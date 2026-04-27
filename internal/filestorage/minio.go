package filestorage

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.27.0"
	"go.opentelemetry.io/otel/trace"
)

const tracerName = "filestorage/minio"

func startSpan(ctx context.Context, op, bucket, key string) (context.Context, trace.Span) {
	return otel.Tracer(tracerName).Start(ctx, "minio."+op,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			semconv.RPCSystemKey.String("minio"),
			attribute.String("storage.operation", op),
			attribute.String("storage.bucket", bucket),
			attribute.String("storage.object_key", key),
		),
	)
}

func endSpan(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}

// MinioStorage implements FileStorage using MinIO object storage.
type MinioStorage struct {
	client     *minio.Client
	bucketName string
}

// NewMinioStorage creates a new MinIO-backed file storage.
// It creates the bucket if it does not exist.
func NewMinioStorage(endpoint, accessKey, secretKey, bucket string, useSSL bool) (*MinioStorage, error) {
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create minio client: %w", err)
	}

	ctx := context.Background()
	exists, err := client.BucketExists(ctx, bucket)
	if err != nil {
		return nil, fmt.Errorf("failed to check bucket existence: %w", err)
	}

	if !exists {
		if err := client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
			return nil, fmt.Errorf("failed to create bucket: %w", err)
		}
	}

	return &MinioStorage{
		client:     client,
		bucketName: bucket,
	}, nil
}

// Upload stores data in MinIO under the given object key.
func (s *MinioStorage) Upload(ctx context.Context, objectKey string, data []byte) (err error) {
	ctx, span := startSpan(ctx, "upload", s.bucketName, objectKey)
	span.SetAttributes(attribute.Int("storage.object_size_bytes", len(data)))
	defer func() { endSpan(span, err) }()

	_, err = s.client.PutObject(ctx, s.bucketName, objectKey, bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{
		ContentType: "application/octet-stream",
	})
	if err != nil {
		return fmt.Errorf("failed to upload object %s: %w", objectKey, err)
	}
	return nil
}

// Download retrieves data from MinIO by object key.
func (s *MinioStorage) Download(ctx context.Context, objectKey string) (data []byte, err error) {
	ctx, span := startSpan(ctx, "download", s.bucketName, objectKey)
	defer func() { endSpan(span, err) }()

	obj, err := s.client.GetObject(ctx, s.bucketName, objectKey, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get object %s: %w", objectKey, err)
	}
	defer obj.Close()

	data, err = io.ReadAll(obj)
	if err != nil {
		return nil, fmt.Errorf("failed to read object %s: %w", objectKey, err)
	}
	span.SetAttributes(attribute.Int("storage.object_size_bytes", len(data)))
	return data, nil
}

// Delete removes an object from MinIO by key.
func (s *MinioStorage) Delete(ctx context.Context, objectKey string) (err error) {
	ctx, span := startSpan(ctx, "delete", s.bucketName, objectKey)
	defer func() { endSpan(span, err) }()

	err = s.client.RemoveObject(ctx, s.bucketName, objectKey, minio.RemoveObjectOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete object %s: %w", objectKey, err)
	}
	return nil
}
