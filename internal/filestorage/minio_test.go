package filestorage

import (
	"bytes"
	"context"
	"testing"
	"time"

	tcminio "github.com/testcontainers/testcontainers-go/modules/minio"
)

const (
	testImage  = "minio/minio:RELEASE.2024-01-16T16-07-38Z"
	testBucket = "test-bucket"
)

func newTestStorage(t *testing.T) *MinioStorage {
	t.Helper()

	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)

	container, err := tcminio.Run(ctx, testImage,
		tcminio.WithUsername("minio_user"),
		tcminio.WithPassword("minio_password"),
	)
	if err != nil {
		t.Fatalf("start minio container: %v", err)
	}
	t.Cleanup(func() {
		if err := container.Terminate(context.Background()); err != nil {
			t.Logf("terminate minio container: %v", err)
		}
	})

	endpoint, err := container.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	storage, err := NewMinioStorage(endpoint, container.Username, container.Password, testBucket, false)
	if err != nil {
		t.Fatalf("NewMinioStorage: %v", err)
	}
	return storage
}

func TestMinioStorage_UploadDownload(t *testing.T) {
	s := newTestStorage(t)
	ctx := context.Background()

	want := []byte("hello, minio")
	key := "users/42/avatar.bin"

	if err := s.Upload(ctx, key, want); err != nil {
		t.Fatalf("Upload: %v", err)
	}

	got, err := s.Download(ctx, key)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("Download returned %q, want %q", got, want)
	}
}

func TestMinioStorage_Delete(t *testing.T) {
	s := newTestStorage(t)
	ctx := context.Background()

	key := "users/42/to-delete.bin"
	if err := s.Upload(ctx, key, []byte("gone soon")); err != nil {
		t.Fatalf("Upload: %v", err)
	}

	if err := s.Delete(ctx, key); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := s.Download(ctx, key); err == nil {
		t.Error("Download after Delete returned nil error, want error")
	}
}

func TestMinioStorage_DownloadMissing(t *testing.T) {
	s := newTestStorage(t)
	ctx := context.Background()

	if _, err := s.Download(ctx, "does/not/exist"); err == nil {
		t.Error("Download of missing key returned nil error, want error")
	}
}

func TestNewMinioStorage_CreatesBucket(t *testing.T) {
	s := newTestStorage(t)
	if err := s.Upload(context.Background(), "probe", []byte("x")); err != nil {
		t.Fatalf("Upload into freshly-created bucket: %v", err)
	}
}
