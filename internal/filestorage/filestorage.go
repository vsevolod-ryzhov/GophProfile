package filestorage

import "context"

//go:generate mockery
type FileStorage interface {
	Upload(ctx context.Context, objKey string, data []byte) error
	Download(ctx context.Context, objKey string) ([]byte, error)
	Delete(ctx context.Context, objKey string) error
}
