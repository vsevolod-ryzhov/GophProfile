package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/golang-migrate/migrate/v4/source/github"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// Storage defines the interface for persistent data operations.
//
//go:generate mockery
type Storage interface {
	Ping(ctx context.Context) error
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
