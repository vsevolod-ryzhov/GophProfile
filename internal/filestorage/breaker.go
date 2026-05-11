package filestorage

import (
	"context"
	"errors"

	"GophProfile/internal/breaker"

	"github.com/minio/minio-go/v7"
)

// BreakerFileStorage wraps a FileStorage with a circuit breaker
type BreakerFileStorage struct {
	inner FileStorage
	b     *breaker.Breaker
}

func NewBreakerFileStorage(inner FileStorage, b *breaker.Breaker) *BreakerFileStorage {
	return &BreakerFileStorage{inner: inner, b: b}
}

// IsMinioFailure excludes "object not found" from the failure count — a missing key is a normal application outcome, not an outage
func IsMinioFailure(err error) bool {
	if err == nil {
		return false
	}
	var resp minio.ErrorResponse
	if errors.As(err, &resp) && resp.Code == "NoSuchKey" {
		return false
	}
	return breaker.DefaultIsFailure(err)
}

func (s *BreakerFileStorage) Upload(ctx context.Context, objKey string, data []byte) error {
	return s.b.Do(func() error { return s.inner.Upload(ctx, objKey, data) })
}

func (s *BreakerFileStorage) Download(ctx context.Context, objKey string) ([]byte, error) {
	return breaker.DoTyped(s.b, func() ([]byte, error) {
		return s.inner.Download(ctx, objKey)
	})
}

func (s *BreakerFileStorage) Delete(ctx context.Context, objKey string) error {
	return s.b.Do(func() error { return s.inner.Delete(ctx, objKey) })
}

var _ FileStorage = (*BreakerFileStorage)(nil)
