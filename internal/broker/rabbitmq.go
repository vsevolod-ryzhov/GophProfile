package broker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"log/slog"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.27.0"
	"go.opentelemetry.io/otel/trace"
)

const tracerName = "broker/rabbitmq"

// amqpHeaderCarrier adapts amqp.Table to the TextMapCarrier interface so the W3C trace context can ride along in message headers.
type amqpHeaderCarrier amqp.Table

func (c amqpHeaderCarrier) Get(key string) string {
	if v, ok := c[key].(string); ok {
		return v
	}
	return ""
}
func (c amqpHeaderCarrier) Set(key, value string) { c[key] = value }
func (c amqpHeaderCarrier) Keys() []string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	return keys
}

var _ propagation.TextMapCarrier = amqpHeaderCarrier(nil)

// startConsumerSpan extracts the upstream trace context from the message headers.
// The handler is expected to start its own processing span using the returned context — that way the span lifetime tracks message handling.
func startConsumerSpan(parent context.Context, msg amqp.Delivery) context.Context {
	carrier := amqpHeaderCarrier(msg.Headers)
	if carrier == nil {
		carrier = amqpHeaderCarrier{}
	}
	return otel.GetTextMapPropagator().Extract(parent, carrier)
}

const (
	exchangeName = "avatars.exchange"
	deleteKey    = "avatar.deleted"
	uploadKey    = "avatar.uploaded"

	deleteQueue = "avatar.delete.queue"
	uploadQueue = "avatar.upload.queue"

	reconnectBaseDelay = time.Second
	reconnectMaxDelay  = 30 * time.Second
)

// ErrBrokerUnavailable is returned by Publish* when the broker is disconnected and the reconnect loop has not yet restored the channel.
var ErrBrokerUnavailable = errors.New("rabbitmq broker unavailable")

// topologyFunc declares exchange/queues/bindings on a freshly opened channel. Called on every (re)connect so the broker comes up to spec even after a restart.
type topologyFunc func(ch *amqp.Channel) error

func declarePublisherTopology(ch *amqp.Channel) error {
	return ch.ExchangeDeclare(exchangeName, "direct", true, false, false, false, nil)
}

func declareConsumerTopology(ch *amqp.Channel) error {
	if err := ch.ExchangeDeclare(exchangeName, "direct", true, false, false, false, nil); err != nil {
		return fmt.Errorf("declare exchange: %w", err)
	}
	if _, err := ch.QueueDeclare(deleteQueue, true, false, false, false, nil); err != nil {
		return fmt.Errorf("declare delete queue: %w", err)
	}
	if err := ch.QueueBind(deleteQueue, deleteKey, exchangeName, false, nil); err != nil {
		return fmt.Errorf("bind delete queue: %w", err)
	}
	if _, err := ch.QueueDeclare(uploadQueue, true, false, false, false, nil); err != nil {
		return fmt.Errorf("declare upload queue: %w", err)
	}
	if err := ch.QueueBind(uploadQueue, uploadKey, exchangeName, false, nil); err != nil {
		return fmt.Errorf("bind upload queue: %w", err)
	}
	return nil
}

// connection wraps a shared dial+channel+topology lifecycle used by both the publisher and the consumer.
type connection struct {
	url      string
	topology topologyFunc
	logger   *slog.Logger

	mu   sync.RWMutex
	conn *amqp.Connection
	ch   *amqp.Channel

	done   chan struct{}
	closed bool
	wg     sync.WaitGroup
}

func newConnection(url string, topology topologyFunc, logger *slog.Logger) (*connection, error) {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	c := &connection{
		url:      url,
		topology: topology,
		logger:   logger,
		done:     make(chan struct{}),
	}
	if err := c.dial(); err != nil {
		return nil, err
	}
	c.wg.Add(1)
	go c.watchdog()
	return c, nil
}

func (c *connection) dial() error {
	conn, err := amqp.Dial(c.url)
	if err != nil {
		return fmt.Errorf("dial rabbitmq: %w", err)
	}
	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return fmt.Errorf("open channel: %w", err)
	}
	if err := c.topology(ch); err != nil {
		ch.Close()
		conn.Close()
		return err
	}

	c.mu.Lock()
	c.conn = conn
	c.ch = ch
	c.mu.Unlock()
	return nil
}

// channel returns the currently-live channel or nil while disconnected.
func (c *connection) channel() *amqp.Channel {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.ch == nil || c.ch.IsClosed() {
		return nil
	}
	return c.ch
}

// watchdog listens for connection loss and reconnects with exponential backoff.
func (c *connection) watchdog() {
	defer c.wg.Done()
	for {
		c.mu.RLock()
		conn := c.conn
		c.mu.RUnlock()
		if conn == nil {
			return
		}

		closeCh := conn.NotifyClose(make(chan *amqp.Error, 1))

		select {
		case <-c.done:
			return
		case err, ok := <-closeCh:
			if !ok || err == nil {
				// Graceful close — either Close() ran or the broker sent a normal shutdown.
				return
			}
			c.logger.Warn("rabbitmq connection lost", "err", err)
		}

		delay := reconnectBaseDelay
		for {
			select {
			case <-c.done:
				return
			case <-time.After(delay):
			}
			if err := c.dial(); err != nil {
				c.logger.Warn("rabbitmq reconnect failed", "retry_in", delay, "err", err)
				delay *= 2
				if delay > reconnectMaxDelay {
					delay = reconnectMaxDelay
				}
				continue
			}
			c.logger.Info("rabbitmq reconnected")
			break
		}
	}
}

func (c *connection) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	close(c.done)
	ch, conn := c.ch, c.conn
	c.ch, c.conn = nil, nil
	c.mu.Unlock()

	var firstErr error
	if ch != nil {
		if err := ch.Close(); err != nil && !errors.Is(err, amqp.ErrClosed) {
			firstErr = err
		}
	}
	if conn != nil {
		if err := conn.Close(); err != nil && !errors.Is(err, amqp.ErrClosed) && firstErr == nil {
			firstErr = err
		}
	}
	c.wg.Wait()
	return firstErr
}

// RabbitPublisher implements Publisher using RabbitMQ with auto-reconnect.
type RabbitPublisher struct {
	*connection
}

func NewRabbitPublisher(url string, logger *slog.Logger) (*RabbitPublisher, error) {
	c, err := newConnection(url, declarePublisherTopology, logger)
	if err != nil {
		return nil, err
	}
	return &RabbitPublisher{connection: c}, nil
}

func (p *RabbitPublisher) PublishDelete(ctx context.Context, event AvatarDeleteEvent) error {
	return p.publish(ctx, deleteKey, event)
}

func (p *RabbitPublisher) PublishUpload(ctx context.Context, event AvatarUploadEvent) error {
	return p.publish(ctx, uploadKey, event)
}

func (p *RabbitPublisher) publish(ctx context.Context, routingKey string, event any) (err error) {
	ctx, span := otel.Tracer(tracerName).Start(ctx, "rabbitmq.publish "+routingKey,
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(
			semconv.MessagingSystemKey.String("rabbitmq"),
			semconv.MessagingDestinationName(exchangeName),
			attribute.String("messaging.rabbitmq.routing_key", routingKey),
			attribute.String("messaging.operation", "publish"),
		),
	)
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	body, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	ch := p.channel()
	if ch == nil {
		return ErrBrokerUnavailable
	}

	headers := amqp.Table{}
	otel.GetTextMapPropagator().Inject(ctx, amqpHeaderCarrier(headers))

	return ch.PublishWithContext(ctx, exchangeName, routingKey, false, false, amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent,
		Headers:      headers,
		Body:         body,
	})
}

// RabbitConsumer implements Consumer using RabbitMQ with auto-reconnect.
type RabbitConsumer struct {
	*connection
}

func NewRabbitConsumer(url string, logger *slog.Logger) (*RabbitConsumer, error) {
	c, err := newConnection(url, declareConsumerTopology, logger)
	if err != nil {
		return nil, err
	}
	return &RabbitConsumer{connection: c}, nil
}

func (c *RabbitConsumer) ConsumeDeletes(ctx context.Context) (<-chan DeleteDelivery, error) {
	out := make(chan DeleteDelivery)
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		defer close(out)
		c.consumeLoop(ctx, deleteQueue, func(msg amqp.Delivery) bool {
			var event AvatarDeleteEvent
			if err := json.Unmarshal(msg.Body, &event); err != nil {
				msg.Nack(false, false)
				return true
			}
			msgCtx := startConsumerSpan(ctx, msg)
			d := DeleteDelivery{
				Context: msgCtx,
				Event:   event,
				Ack:     func() error { return msg.Ack(false) },
				Nack:    func() error { return msg.Nack(false, true) },
			}
			select {
			case out <- d:
				return true
			case <-ctx.Done():
				return false
			case <-c.done:
				return false
			}
		})
	}()
	return out, nil
}

func (c *RabbitConsumer) ConsumeUploads(ctx context.Context) (<-chan UploadDelivery, error) {
	out := make(chan UploadDelivery)
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		defer close(out)
		c.consumeLoop(ctx, uploadQueue, func(msg amqp.Delivery) bool {
			var event AvatarUploadEvent
			if err := json.Unmarshal(msg.Body, &event); err != nil {
				msg.Nack(false, false)
				return true
			}
			msgCtx := startConsumerSpan(ctx, msg)
			d := UploadDelivery{
				Context: msgCtx,
				Event:   event,
				Ack:     func() error { return msg.Ack(false) },
				Nack:    func() error { return msg.Nack(false, true) },
			}
			select {
			case out <- d:
				return true
			case <-ctx.Done():
				return false
			case <-c.done:
				return false
			}
		})
	}()
	return out, nil
}

// consumeLoop keeps a subscription alive across reconnects. `handle` returns false when the caller's ctx is done and the loop should exit.
func (c *RabbitConsumer) consumeLoop(ctx context.Context, queue string, handle func(amqp.Delivery) bool) {
	for {
		if ctx.Err() != nil {
			return
		}
		select {
		case <-c.done:
			return
		default:
		}

		ch := c.channel()
		if ch == nil {
			if !sleep(ctx, c.done, reconnectBaseDelay) {
				return
			}
			continue
		}

		msgs, err := ch.ConsumeWithContext(ctx, queue, "", false, false, false, false, nil)
		if err != nil {
			c.logger.Warn("failed to start consuming", "queue", queue, "err", err)
			if !sleep(ctx, c.done, reconnectBaseDelay) {
				return
			}
			continue
		}

		for msg := range msgs {
			if !handle(msg) {
				return
			}
		}
		// msgs closed: either ctx cancellation, Close(), or connection loss.
		// The outer loop re-checks ctx/done and picks up the new channel once the watchdog has reconnected.
	}
}

// sleep waits for `d`, returning false if ctx or done fires first.
func sleep(ctx context.Context, done <-chan struct{}, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	case <-done:
		return false
	}
}
