package broker

import (
	"context"
	"fmt"
	"log"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/rabbitmq"
	"go.uber.org/zap"
)

var testRabbitURL string

func TestMain(m *testing.M) {
	ctx := context.Background()

	container, err := rabbitmq.Run(ctx,
		"rabbitmq:3.12-management-alpine",
		rabbitmq.WithAdminUsername("guest"),
		rabbitmq.WithAdminPassword("guest"),
	)
	if err != nil {
		log.Fatalf("failed to start rabbitmq container: %s", err)
	}

	testRabbitURL, err = container.AmqpURL(ctx)
	if err != nil {
		log.Fatalf("failed to get amqp url: %s", err)
	}

	code := m.Run()

	if err := testcontainers.TerminateContainer(container); err != nil {
		log.Printf("failed to terminate rabbitmq container: %s", err)
	}

	os.Exit(code)
}

func newPubConsumer(t *testing.T) (*RabbitPublisher, *RabbitConsumer) {
	t.Helper()
	consumer, err := NewRabbitConsumer(testRabbitURL, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { consumer.Close() })

	pub, err := NewRabbitPublisher(testRabbitURL, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { pub.Close() })

	return pub, consumer
}

// drainChannel reads all pending messages from a channel with a short timeout.
func drainChannel[T any](ch <-chan T, timeout time.Duration) {
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-time.After(timeout):
			return
		}
	}
}

func TestPublishAndConsumeDelete(t *testing.T) {
	pub, consumer := newPubConsumer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	deliveries, err := consumer.ConsumeDeletes(ctx)
	require.NoError(t, err)

	// Drain any leftover messages from previous tests
	drainChannel(deliveries, 200*time.Millisecond)

	sent := AvatarDeleteEvent{
		AvatarID: "avatar-del-1",
		S3Keys:   []string{"key1", "key2"},
	}
	require.NoError(t, pub.PublishDelete(ctx, sent))

	select {
	case d := <-deliveries:
		assert.Equal(t, sent.AvatarID, d.Event.AvatarID)
		assert.Equal(t, sent.S3Keys, d.Event.S3Keys)
		assert.NoError(t, d.Ack())
	case <-ctx.Done():
		t.Fatal("timed out waiting for delete delivery")
	}
}

func TestPublishAndConsumeUpload(t *testing.T) {
	pub, consumer := newPubConsumer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	deliveries, err := consumer.ConsumeUploads(ctx)
	require.NoError(t, err)

	drainChannel(deliveries, 200*time.Millisecond)

	sent := AvatarUploadEvent{
		AvatarID: "avatar-upl-1",
		UserID:   "user-42",
		S3Key:    "users/42/avatar-upl-1",
	}
	require.NoError(t, pub.PublishUpload(ctx, sent))

	select {
	case d := <-deliveries:
		assert.Equal(t, sent.AvatarID, d.Event.AvatarID)
		assert.Equal(t, sent.UserID, d.Event.UserID)
		assert.Equal(t, sent.S3Key, d.Event.S3Key)
		assert.NoError(t, d.Ack())
	case <-ctx.Done():
		t.Fatal("timed out waiting for upload delivery")
	}
}

func TestDelivery_Nack_Requeues(t *testing.T) {
	pub, consumer := newPubConsumer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	deliveries, err := consumer.ConsumeDeletes(ctx)
	require.NoError(t, err)

	drainChannel(deliveries, 200*time.Millisecond)

	sent := AvatarDeleteEvent{AvatarID: "nack-test", S3Keys: []string{"k1"}}
	require.NoError(t, pub.PublishDelete(ctx, sent))

	// First delivery — nack it (requeue=true)
	select {
	case d := <-deliveries:
		assert.Equal(t, sent.AvatarID, d.Event.AvatarID)
		assert.NoError(t, d.Nack())
	case <-ctx.Done():
		t.Fatal("timed out waiting for first delivery")
	}

	// Should be redelivered
	select {
	case d := <-deliveries:
		assert.Equal(t, sent.AvatarID, d.Event.AvatarID)
		assert.NoError(t, d.Ack())
	case <-ctx.Done():
		t.Fatal("timed out waiting for redelivered message")
	}
}

func TestMultipleMessages(t *testing.T) {
	pub, consumer := newPubConsumer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	deliveries, err := consumer.ConsumeDeletes(ctx)
	require.NoError(t, err)

	drainChannel(deliveries, 200*time.Millisecond)

	count := 5
	for i := 0; i < count; i++ {
		event := AvatarDeleteEvent{
			AvatarID: fmt.Sprintf("multi-%d", i),
			S3Keys:   []string{fmt.Sprintf("key-%d", i)},
		}
		require.NoError(t, pub.PublishDelete(ctx, event))
	}

	received := make(map[string]bool)
	for i := 0; i < count; i++ {
		select {
		case d := <-deliveries:
			received[d.Event.AvatarID] = true
			assert.NoError(t, d.Ack())
		case <-ctx.Done():
			t.Fatalf("timed out after receiving %d/%d messages", i, count)
		}
	}

	assert.Len(t, received, count)
}

func TestNewRabbitPublisher_BadURL(t *testing.T) {
	_, err := NewRabbitPublisher("amqp://bad:bad@localhost:1/", zap.NewNop())
	assert.Error(t, err)
}

func TestNewRabbitConsumer_BadURL(t *testing.T) {
	_, err := NewRabbitConsumer("amqp://bad:bad@localhost:1/", zap.NewNop())
	assert.Error(t, err)
}
