package model

import (
	"time"

	"github.com/google/uuid"
)

type Avatar struct {
	ID               uuid.UUID `json:"id"`
	UserID           string    `json:"user_id"`
	FileName         string    `json:"file_name"`
	MimeType         string    `json:"mime_type"`
	SizeBytes        int64     `json:"size_bytes"`
	S3Key            string    `json:"s3_key"`
	ThumbnailS3Keys  []string  `json:"thumbnail_s3_keys"`
	UploadStatus     string    `json:"upload_status"`
	ProcessingStatus string    `json:"processing_status"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
	DeletedAt        time.Time `json:"deleted_at"`
}
