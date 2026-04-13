package broker

import (
	"context"
	"encoding/json"
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	exchangeName = "avatars.exchange"
	deleteKey    = "avatar.deleted"
)

// RabbitPublisher implements Publisher using RabbitMQ.
type RabbitPublisher struct {
	conn *amqp.Connection
	ch   *amqp.Channel
}

// NewRabbitPublisher connects to RabbitMQ, declares the exchange, and returns a ready publisher.
func NewRabbitPublisher(url string) (*RabbitPublisher, error) {
	conn, err := amqp.Dial(url)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to rabbitmq: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to open channel: %w", err)
	}

	if err := ch.ExchangeDeclare(
		exchangeName,
		"direct",
		true,
		false,
		false,
		false,
		nil,
	); err != nil {
		ch.Close()
		conn.Close()
		return nil, fmt.Errorf("failed to declare exchange: %w", err)
	}

	return &RabbitPublisher{conn: conn, ch: ch}, nil
}

func (p *RabbitPublisher) PublishDelete(ctx context.Context, event AvatarDeleteEvent) error {
	body, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal delete event: %w", err)
	}

	return p.ch.PublishWithContext(ctx,
		exchangeName,
		deleteKey,
		false,
		false,
		amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent,
			Body:         body,
		},
	)
}

func (p *RabbitPublisher) Close() error {
	if err := p.ch.Close(); err != nil {
		p.conn.Close()
		return err
	}
	return p.conn.Close()
}

const deleteQueue = "avatar.delete.queue"

// RabbitConsumer implements Consumer using RabbitMQ.
type RabbitConsumer struct {
	conn *amqp.Connection
	ch   *amqp.Channel
}

// NewRabbitConsumer connects to RabbitMQ, declares the exchange and queue, binds the queue to the delete routing key, and returns a ready consumer
func NewRabbitConsumer(url string) (*RabbitConsumer, error) {
	conn, err := amqp.Dial(url)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to rabbitmq: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to open channel: %w", err)
	}

	if err := ch.ExchangeDeclare(
		exchangeName,
		"direct",
		true,
		false,
		false,
		false,
		nil,
	); err != nil {
		ch.Close()
		conn.Close()
		return nil, fmt.Errorf("failed to declare exchange: %w", err)
	}

	if _, err := ch.QueueDeclare(
		deleteQueue,
		true,
		false,
		false,
		false,
		nil,
	); err != nil {
		ch.Close()
		conn.Close()
		return nil, fmt.Errorf("failed to declare queue: %w", err)
	}

	if err := ch.QueueBind(deleteQueue, deleteKey, exchangeName, false, nil); err != nil {
		ch.Close()
		conn.Close()
		return nil, fmt.Errorf("failed to bind queue: %w", err)
	}

	return &RabbitConsumer{conn: conn, ch: ch}, nil
}

// ConsumeDeletes returns a channel of Delivery values. The caller is responsible for calling Ack() after successful processing or Nack() on failure. The channel is closed when ctx is cancelled.
func (c *RabbitConsumer) ConsumeDeletes(ctx context.Context) (<-chan Delivery, error) {
	msgs, err := c.ch.ConsumeWithContext(ctx,
		deleteQueue,
		"",
		false,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to start consuming: %w", err)
	}

	out := make(chan Delivery)
	go func() {
		defer close(out)
		for msg := range msgs {
			var event AvatarDeleteEvent
			if err := json.Unmarshal(msg.Body, &event); err != nil {
				msg.Nack(false, false)
				continue
			}
			d := Delivery{
				Event: event,
				Ack:   func() error { return msg.Ack(false) },
				Nack:  func() error { return msg.Nack(false, true) },
			}
			select {
			case out <- d:
			case <-ctx.Done():
				return
			}
		}
	}()

	return out, nil
}

func (c *RabbitConsumer) Close() error {
	if err := c.ch.Close(); err != nil {
		c.conn.Close()
		return err
	}
	return c.conn.Close()
}
