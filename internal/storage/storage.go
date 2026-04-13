package storage

import (
	"GophProfile/internal/model"
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/golang-migrate/migrate/v4/source/github"
	_ "github.com/jackc/pgx/v5/stdlib"

	sq "github.com/Masterminds/squirrel"
)

// Storage defines the interface for persistent data operations.
//
//go:generate mockery
type Storage interface {
	Ping(ctx context.Context) error
	CreateNewAvatarRecord(ctx context.Context, userID, fileName, mimeType string, size int64) (*model.Avatar, error)
	UpdateAvatarS3Key(ctx context.Context, avatarID string, s3Key string) error
}

// PostgresStorage implements the Storage interface using PostgreSQL.
type PostgresStorage struct {
	db *sql.DB
}

// NewPostgresStorage connects to the database, applies migrations, and returns a new PostgresStorage.
func NewPostgresStorage(connectionString string) (*PostgresStorage, error) {
	db, err := sql.Open("pgx", connectionString)
	if err != nil {
		return nil, err
	}

	if errPing := db.Ping(); errPing != nil {
		return nil, errPing
	}

	migrationDB, err := sql.Open("pgx", connectionString)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to open migration database: %w", err)
	}
	defer migrationDB.Close()

	if errMigrations := applyMigrations(migrationDB); errMigrations != nil {
		db.Close()
		return nil, fmt.Errorf("failed to apply migrations: %w", errMigrations)
	}

	return &PostgresStorage{db: db}, nil
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
	defer m.Close()

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
