package storage

import (
	"context"
	"database/sql"
	"errors"

	"GophProfile/internal/breaker"
	"GophProfile/internal/model"
)

// BreakerStorage wraps a Storage with a circuit breaker. Postgres outages trip fast-failing locally instead of stalling requests behind connection timeouts
type BreakerStorage struct {
	inner Storage
	b     *breaker.Breaker
}

func NewBreakerStorage(inner Storage, b *breaker.Breaker) *BreakerStorage {
	return &BreakerStorage{inner: inner, b: b}
}

// IsPostgresFailure excludes "row not found" from the failure count — that's a normal application response, not a sign the DB is unhealthy
func IsPostgresFailure(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false
	}
	return breaker.DefaultIsFailure(err)
}

func (s *BreakerStorage) Ping(ctx context.Context) error {
	return s.b.Do(func() error { return s.inner.Ping(ctx) })
}

func (s *BreakerStorage) CreateNewAvatarRecord(ctx context.Context, userID, fileName, mimeType string, size int64) (*model.Avatar, error) {
	return breaker.DoTyped(s.b, func() (*model.Avatar, error) {
		return s.inner.CreateNewAvatarRecord(ctx, userID, fileName, mimeType, size)
	})
}

func (s *BreakerStorage) UpdateAvatarS3Key(ctx context.Context, avatarID string, s3Key string) error {
	return s.b.Do(func() error { return s.inner.UpdateAvatarS3Key(ctx, avatarID, s3Key) })
}

func (s *BreakerStorage) GetAvatarByID(ctx context.Context, avatarID string) (*model.Avatar, error) {
	return breaker.DoTyped(s.b, func() (*model.Avatar, error) {
		return s.inner.GetAvatarByID(ctx, avatarID)
	})
}

func (s *BreakerStorage) ListAvatarsByUserID(ctx context.Context, userID string) ([]model.Avatar, error) {
	return breaker.DoTyped(s.b, func() ([]model.Avatar, error) {
		return s.inner.ListAvatarsByUserID(ctx, userID)
	})
}

func (s *BreakerStorage) SoftDeleteAvatar(ctx context.Context, avatarID string) error {
	return s.b.Do(func() error { return s.inner.SoftDeleteAvatar(ctx, avatarID) })
}

func (s *BreakerStorage) UpdateThumbnailKeys(ctx context.Context, avatarID string, keys []string) error {
	return s.b.Do(func() error { return s.inner.UpdateThumbnailKeys(ctx, avatarID, keys) })
}

func (s *BreakerStorage) UpdateProcessingStatus(ctx context.Context, avatarID string, status string) error {
	return s.b.Do(func() error { return s.inner.UpdateProcessingStatus(ctx, avatarID, status) })
}

// Compile-time assertion that BreakerStorage satisfies Storage.
var _ Storage = (*BreakerStorage)(nil)
