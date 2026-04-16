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

// newTestStorage creates a PostgresStorage backed by a real Postgres container.
func newTestStorage(t *testing.T) *PostgresStorage {
	t.Helper()
	t.Chdir("../..")
	dsn := newTestPostgres(t)
	s, err := NewPostgresStorage(dsn)
	if err != nil {
		t.Fatalf("NewPostgresStorage: %v", err)
	}
	t.Cleanup(func() { s.db.Close() })
	return s
}

func TestCreateNewAvatarRecord(t *testing.T) {
	s := newTestStorage(t)
	ctx := context.Background()

	avatar, err := s.CreateNewAvatarRecord(ctx, "user-1", "photo.jpg", "image/jpeg", 12345)
	if err != nil {
		t.Fatalf("CreateNewAvatarRecord: %v", err)
	}

	if avatar.ID.String() == "" {
		t.Error("expected non-empty avatar ID")
	}
	if avatar.UserID != "user-1" {
		t.Errorf("UserID = %q, want %q", avatar.UserID, "user-1")
	}
	if avatar.FileName != "photo.jpg" {
		t.Errorf("FileName = %q, want %q", avatar.FileName, "photo.jpg")
	}
	if avatar.MimeType != "image/jpeg" {
		t.Errorf("MimeType = %q, want %q", avatar.MimeType, "image/jpeg")
	}
	if avatar.SizeBytes != 12345 {
		t.Errorf("SizeBytes = %d, want %d", avatar.SizeBytes, 12345)
	}
	if avatar.UploadStatus != "uploading" {
		t.Errorf("UploadStatus = %q, want %q", avatar.UploadStatus, "uploading")
	}
	if avatar.ProcessingStatus != "pending" {
		t.Errorf("ProcessingStatus = %q, want %q", avatar.ProcessingStatus, "pending")
	}
	if avatar.CreatedAt.IsZero() {
		t.Error("CreatedAt should not be zero")
	}
}

func TestGetAvatarByID(t *testing.T) {
	s := newTestStorage(t)
	ctx := context.Background()

	created, err := s.CreateNewAvatarRecord(ctx, "user-2", "img.png", "image/png", 999)
	if err != nil {
		t.Fatalf("CreateNewAvatarRecord: %v", err)
	}

	got, err := s.GetAvatarByID(ctx, created.ID.String())
	if err != nil {
		t.Fatalf("GetAvatarByID: %v", err)
	}

	if got.ID != created.ID {
		t.Errorf("ID = %v, want %v", got.ID, created.ID)
	}
	if got.UserID != "user-2" {
		t.Errorf("UserID = %q, want %q", got.UserID, "user-2")
	}
	if len(got.ThumbnailS3Keys) != 0 {
		t.Errorf("ThumbnailS3Keys = %v, want empty", got.ThumbnailS3Keys)
	}
}

func TestGetAvatarByID_NotFound(t *testing.T) {
	s := newTestStorage(t)
	ctx := context.Background()

	_, err := s.GetAvatarByID(ctx, "00000000-0000-0000-0000-000000000000")
	if err == nil {
		t.Fatal("expected error for non-existent avatar")
	}
}

func TestUpdateAvatarS3Key(t *testing.T) {
	s := newTestStorage(t)
	ctx := context.Background()

	avatar, _ := s.CreateNewAvatarRecord(ctx, "user-3", "a.jpg", "image/jpeg", 100)

	err := s.UpdateAvatarS3Key(ctx, avatar.ID.String(), "user-3/"+avatar.ID.String())
	if err != nil {
		t.Fatalf("UpdateAvatarS3Key: %v", err)
	}

	got, _ := s.GetAvatarByID(ctx, avatar.ID.String())
	wantKey := "user-3/" + avatar.ID.String()
	if got.S3Key != wantKey {
		t.Errorf("S3Key = %q, want %q", got.S3Key, wantKey)
	}
	if got.UploadStatus != "uploaded" {
		t.Errorf("UploadStatus = %q, want %q", got.UploadStatus, "uploaded")
	}
}

func TestUpdateThumbnailKeys(t *testing.T) {
	s := newTestStorage(t)
	ctx := context.Background()

	avatar, _ := s.CreateNewAvatarRecord(ctx, "user-4", "b.jpg", "image/jpeg", 200)

	keys := []string{"thumbnails/100x100.jpg", "thumbnails/300x300.jpg"}
	err := s.UpdateThumbnailKeys(ctx, avatar.ID.String(), keys)
	if err != nil {
		t.Fatalf("UpdateThumbnailKeys: %v", err)
	}

	got, _ := s.GetAvatarByID(ctx, avatar.ID.String())
	if len(got.ThumbnailS3Keys) != 2 {
		t.Fatalf("ThumbnailS3Keys len = %d, want 2", len(got.ThumbnailS3Keys))
	}
	if got.ThumbnailS3Keys[0] != keys[0] || got.ThumbnailS3Keys[1] != keys[1] {
		t.Errorf("ThumbnailS3Keys = %v, want %v", got.ThumbnailS3Keys, keys)
	}
}

func TestUpdateProcessingStatus(t *testing.T) {
	s := newTestStorage(t)
	ctx := context.Background()

	avatar, _ := s.CreateNewAvatarRecord(ctx, "user-5", "c.jpg", "image/jpeg", 300)

	err := s.UpdateProcessingStatus(ctx, avatar.ID.String(), "completed")
	if err != nil {
		t.Fatalf("UpdateProcessingStatus: %v", err)
	}

	got, _ := s.GetAvatarByID(ctx, avatar.ID.String())
	if got.ProcessingStatus != "completed" {
		t.Errorf("ProcessingStatus = %q, want %q", got.ProcessingStatus, "completed")
	}
}

func TestSoftDeleteAvatar(t *testing.T) {
	s := newTestStorage(t)
	ctx := context.Background()

	avatar, _ := s.CreateNewAvatarRecord(ctx, "user-6", "d.jpg", "image/jpeg", 400)

	err := s.SoftDeleteAvatar(ctx, avatar.ID.String())
	if err != nil {
		t.Fatalf("SoftDeleteAvatar: %v", err)
	}

	// GetAvatarByID filters deleted_at IS NULL, so it should return not found
	_, err = s.GetAvatarByID(ctx, avatar.ID.String())
	if err == nil {
		t.Fatal("expected error after soft delete, got nil")
	}
}

func TestSoftDeleteAvatar_Idempotent(t *testing.T) {
	s := newTestStorage(t)
	ctx := context.Background()

	avatar, _ := s.CreateNewAvatarRecord(ctx, "user-7", "e.jpg", "image/jpeg", 500)

	// Delete twice should not error
	if err := s.SoftDeleteAvatar(ctx, avatar.ID.String()); err != nil {
		t.Fatalf("first SoftDeleteAvatar: %v", err)
	}
	if err := s.SoftDeleteAvatar(ctx, avatar.ID.String()); err != nil {
		t.Fatalf("second SoftDeleteAvatar: %v", err)
	}
}
