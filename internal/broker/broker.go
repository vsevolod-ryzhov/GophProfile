package broker

import "context"

// AvatarDeleteEvent is published when a user requests avatar deletion.
type AvatarDeleteEvent struct {
	AvatarID string   `json:"avatar_id"`
	S3Keys   []string `json:"s3_keys"`
}

// Publisher sends events to a message broker.
//
//go:generate mockery --name=Publisher --output=mocks --outpkg=mocks
type Publisher interface {
	PublishDelete(ctx context.Context, event AvatarDeleteEvent) error
	Close() error
}

// Delivery wraps an event with Ack/Nack control so the caller can acknowledge only after successful processing
type Delivery struct {
	Event AvatarDeleteEvent
	Ack   func() error
	Nack  func() error
}

// Consumer receives events from a message broker.
type Consumer interface {
	// ConsumeDeletes returns a channel of Delivery messages. The caller must call Ack() or Nack() on each delivery. The channel is closed when ctx is cancelled.
	ConsumeDeletes(ctx context.Context) (<-chan Delivery, error)
	Close() error
}
