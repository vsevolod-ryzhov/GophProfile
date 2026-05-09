package storage

import (
	"GophProfile/internal/model"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/XSAM/otelsql"
	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/golang-migrate/migrate/v4/source/github"
	_ "github.com/jackc/pgx/v5/stdlib"
	semconv "go.opentelemetry.io/otel/semconv/v1.27.0"

	sq "github.com/Masterminds/squirrel"
)

// Storage defines the interface for persistent data operations.
//
//go:generate mockery --name=Storage --output=mocks --outpkg=mocks
type Storage interface {
	Ping(ctx context.Context) error
	CreateNewAvatarRecord(ctx context.Context, userID, fileName, mimeType string, size int64) (*model.Avatar, error)
	UpdateAvatarS3Key(ctx context.Context, avatarID string, s3Key string) error
	GetAvatarByID(ctx context.Context, avatarID string) (*model.Avatar, error)
	ListAvatarsByUserID(ctx context.Context, userID string) ([]model.Avatar, error)
	SoftDeleteAvatar(ctx context.Context, avatarID string) error
	UpdateThumbnailKeys(ctx context.Context, avatarID string, keys []string) error
	UpdateProcessingStatus(ctx context.Context, avatarID string, status string) error
}

// PostgresStorage implements the Storage interface using PostgreSQL.
type PostgresStorage struct {
	db *sql.DB
}

// NewPostgresStorage connects to the database, applies migrations, and returns a new PostgresStorage.
func NewPostgresStorage(connectionString string) (*PostgresStorage, error) {
	db, err := otelsql.Open("pgx", connectionString,
		otelsql.WithAttributes(semconv.DBSystemPostgreSQL),
		otelsql.WithSpanOptions(otelsql.SpanOptions{OmitConnResetSession: true}),
	)
	if err != nil {
		return nil, err
	}
	if _, err := otelsql.RegisterDBStatsMetrics(db, otelsql.WithAttributes(semconv.DBSystemPostgreSQL)); err != nil {
		db.Close()
		return nil, fmt.Errorf("register db stats metrics: %w", err)
	}

	if errPing := db.Ping(); errPing != nil {
		db.Close()
		return nil, errPing
	}

	if errMigrations := applyMigrations(db); errMigrations != nil {
		db.Close()
		return nil, fmt.Errorf("failed to apply migrations: %w", errMigrations)
	}

	return &PostgresStorage{db: db}, nil
}

// ApplyMigrationsDSN opens a standalone connection, applies migrations, and closes it.
// Used by cmd/migrate (Helm pre-install/pre-upgrade hook). The server's NewPostgresStorage
// also runs migrations on startup as a no-op safety net when the hook has already run.
func ApplyMigrationsDSN(dsn string) error {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return fmt.Errorf("ping db: %w", err)
	}
	return applyMigrations(db)
}

func applyMigrations(db *sql.DB) error {
	driver, err := postgres.WithInstance(db, &postgres.Config{})
	if err != nil {
		return fmt.Errorf("failed to create migration driver: %w", err)
	}

	m, err := migrate.NewWithDatabaseInstance(
		"file://migrations",
		"postgres",
		driver,
	)
	if err != nil {
		return fmt.Errorf("failed to create migrate instance: %w", err)
	}

	if errUp := m.Up(); errUp != nil && !errors.Is(errUp, migrate.ErrNoChange) {
		return fmt.Errorf("failed to apply migrations: %w", errUp)
	}
	return nil
}

// Ping verifies that the database connection is alive.
func (s *PostgresStorage) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

func (s *PostgresStorage) CreateNewAvatarRecord(ctx context.Context, userID, fileName, mimeType string, size int64) (*model.Avatar, error) {
	var avatar model.Avatar
	err := (sq.Insert("avatars").
		Columns("user_id", "file_name", "mime_type", "size_bytes").
		Values(userID, fileName, mimeType, size).
		Suffix("RETURNING id, user_id, file_name, mime_type, size_bytes, upload_status, processing_status, created_at, updated_at").
		PlaceholderFormat(sq.Dollar).
		RunWith(s.db).
		QueryRowContext(ctx)).
		Scan(&avatar.ID, &avatar.UserID, &avatar.FileName, &avatar.MimeType, &avatar.SizeBytes, &avatar.UploadStatus, &avatar.ProcessingStatus, &avatar.CreatedAt, &avatar.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &avatar, nil
}

func (s *PostgresStorage) GetAvatarByID(ctx context.Context, avatarID string) (*model.Avatar, error) {
	var avatar model.Avatar
	var thumbnailsJSON sql.NullString
	err := sq.Select("id", "user_id", "file_name", "mime_type", "size_bytes", "COALESCE(s3_key, '')",
		"upload_status", "processing_status", "COALESCE(thumbnail_s3_keys, '[]')", "created_at", "updated_at").
		From("avatars").
		Where(sq.Eq{"id": avatarID}).
		Where("deleted_at IS NULL").
		PlaceholderFormat(sq.Dollar).
		RunWith(s.db).
		QueryRowContext(ctx).
		Scan(&avatar.ID, &avatar.UserID, &avatar.FileName, &avatar.MimeType, &avatar.SizeBytes, &avatar.S3Key,
			&avatar.UploadStatus, &avatar.ProcessingStatus, &thumbnailsJSON, &avatar.CreatedAt, &avatar.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if thumbnailsJSON.Valid && thumbnailsJSON.String != "" {
		if err := json.Unmarshal([]byte(thumbnailsJSON.String), &avatar.ThumbnailS3Keys); err != nil {
			return nil, fmt.Errorf("failed to unmarshal thumbnail keys: %w", err)
		}
	}
	return &avatar, nil
}

func (s *PostgresStorage) ListAvatarsByUserID(ctx context.Context, userID string) ([]model.Avatar, error) {
	rows, err := sq.Select("id", "user_id", "file_name", "mime_type", "size_bytes", "COALESCE(s3_key, '')",
		"upload_status", "processing_status", "COALESCE(thumbnail_s3_keys, '[]')", "created_at", "updated_at").
		From("avatars").
		Where(sq.Eq{"user_id": userID}).
		Where("deleted_at IS NULL").
		OrderBy("created_at DESC").
		PlaceholderFormat(sq.Dollar).
		RunWith(s.db).
		QueryContext(ctx)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var avatars []model.Avatar
	for rows.Next() {
		var a model.Avatar
		var thumbnailsJSON sql.NullString
		if err := rows.Scan(&a.ID, &a.UserID, &a.FileName, &a.MimeType, &a.SizeBytes, &a.S3Key,
			&a.UploadStatus, &a.ProcessingStatus, &thumbnailsJSON, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		if thumbnailsJSON.Valid && thumbnailsJSON.String != "" {
			if err := json.Unmarshal([]byte(thumbnailsJSON.String), &a.ThumbnailS3Keys); err != nil {
				return nil, fmt.Errorf("failed to unmarshal thumbnail keys: %w", err)
			}
		}
		avatars = append(avatars, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return avatars, nil
}

func (s *PostgresStorage) SoftDeleteAvatar(ctx context.Context, avatarID string) error {
	_, err := sq.Update("avatars").
		Set("deleted_at", sq.Expr("NOW()")).
		Set("updated_at", sq.Expr("NOW()")).
		Where(sq.Eq{"id": avatarID}).
		Where("deleted_at IS NULL").
		PlaceholderFormat(sq.Dollar).
		RunWith(s.db).
		ExecContext(ctx)
	return err
}

func (s *PostgresStorage) UpdateAvatarS3Key(ctx context.Context, avatarID string, s3Key string) error {
	_, err := sq.Update("avatars").
		Set("s3_key", s3Key).
		Set("upload_status", "uploaded").
		Set("updated_at", sq.Expr("NOW()")).
		Where(sq.Eq{"id": avatarID}).
		PlaceholderFormat(sq.Dollar).
		RunWith(s.db).
		ExecContext(ctx)
	return err
}

func (s *PostgresStorage) UpdateThumbnailKeys(ctx context.Context, avatarID string, keys []string) error {
	keysJSON, err := json.Marshal(keys)
	if err != nil {
		return fmt.Errorf("failed to marshal thumbnail keys: %w", err)
	}
	_, err = sq.Update("avatars").
		Set("thumbnail_s3_keys", string(keysJSON)).
		Set("updated_at", sq.Expr("NOW()")).
		Where(sq.Eq{"id": avatarID}).
		PlaceholderFormat(sq.Dollar).
		RunWith(s.db).
		ExecContext(ctx)
	return err
}

func (s *PostgresStorage) UpdateProcessingStatus(ctx context.Context, avatarID string, status string) error {
	_, err := sq.Update("avatars").
		Set("processing_status", status).
		Set("updated_at", sq.Expr("NOW()")).
		Where(sq.Eq{"id": avatarID}).
		PlaceholderFormat(sq.Dollar).
		RunWith(s.db).
		ExecContext(ctx)
	return err
}
