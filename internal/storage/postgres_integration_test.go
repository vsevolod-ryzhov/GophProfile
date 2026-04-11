package storage

import (
	"context"
	"testing"
	"time"

	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

const testPostgresImage = "postgres:16-alpine"

func newTestPostgres(t *testing.T) string {
	t.Helper()

	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)

	container, err := tcpostgres.Run(ctx, testPostgresImage,
		tcpostgres.WithDatabase("testdb"),
		tcpostgres.WithUsername("testuser"),
		tcpostgres.WithPassword("testpass"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() {
		if err := container.Terminate(context.Background()); err != nil {
			t.Logf("terminate postgres container: %v", err)
		}
	})

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	return dsn
}

func TestNewPostgresStorage_AppliesMigrations(t *testing.T) {
	// applyMigrations uses file://migrations (relative to cwd).
	// go test runs from the package dir, so hop to repo root.
	t.Chdir("../..")

	dsn := newTestPostgres(t)

	s, err := NewPostgresStorage(dsn)
	if err != nil {
		t.Fatalf("NewPostgresStorage: %v", err)
	}
	t.Cleanup(func() { s.db.Close() })

	if err := s.Ping(context.Background()); err != nil {
		t.Errorf("Ping after construction: %v", err)
	}

	var exists bool
	err = s.db.QueryRowContext(context.Background(), `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = 'public' AND table_name = 'avatars'
		)`).Scan(&exists)
	if err != nil {
		t.Fatalf("query table existence: %v", err)
	}
	if !exists {
		t.Error("avatars table was not created by migrations")
	}
}

func TestNewPostgresStorage_MigrationsIdempotent(t *testing.T) {
	t.Chdir("../..")

	dsn := newTestPostgres(t)

	s1, err := NewPostgresStorage(dsn)
	if err != nil {
		t.Fatalf("first NewPostgresStorage: %v", err)
	}
	s1.db.Close()

	s2, err := NewPostgresStorage(dsn)
	if err != nil {
		t.Fatalf("second NewPostgresStorage (migrate.ErrNoChange branch): %v", err)
	}
	t.Cleanup(func() { s2.db.Close() })
}

func TestNewPostgresStorage_BadDSN(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}

	_, err := NewPostgresStorage("host=127.0.0.1 port=1 user=nobody dbname=nope sslmode=disable connect_timeout=1")
	if err == nil {
		t.Fatal("NewPostgresStorage with unreachable DSN returned nil error")
	}
}
