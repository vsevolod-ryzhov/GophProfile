package broker

import "context"

// AvatarDeleteEvent is published when a user requests avatar deletion.
type AvatarDeleteEvent struct {
	AvatarID string   `json:"avatar_id"`
	S3Keys   []string `json:"s3_keys"`
}

// AvatarUploadEvent is published after a new avatar is uploaded to S3.
type AvatarUploadEvent struct {
	AvatarID string `json:"avatar_id"`
	UserID   string `json:"user_id"`
	S3Key    string `json:"s3_key"`
}

// Publisher sends events to a message broker.
//
//go:generate mockery --name=Publisher --output=mocks --outpkg=mocks
type Publisher interface {
	PublishDelete(ctx context.Context, event AvatarDeleteEvent) error
	PublishUpload(ctx context.Context, event AvatarUploadEvent) error
	Close() error
}

// DeleteDelivery wraps a delete event with Ack/Nack control.
// Context carries the trace context extracted from the incoming message headers.
type DeleteDelivery struct {
	Context context.Context
	Event   AvatarDeleteEvent
	Ack     func() error
	Nack    func() error
}

// UploadDelivery wraps an upload event with Ack/Nack control.
// Context carries the trace context extracted from the incoming message headers.
type UploadDelivery struct {
	Context context.Context
	Event   AvatarUploadEvent
	Ack     func() error
	Nack    func() error
}

// Consumer receives events from a message broker.
type Consumer interface {
	ConsumeDeletes(ctx context.Context) (<-chan DeleteDelivery, error)
	ConsumeUploads(ctx context.Context) (<-chan UploadDelivery, error)
	Close() error
}
