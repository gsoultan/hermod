package rabbitmq

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/user/hermod"
)

// RabbitMQQueueSink implements the hermod.Sink interface for RabbitMQ Queue.
type RabbitMQQueueSink struct {
	conn      *amqp.Connection
	channel   *amqp.Channel
	url       string
	queue     string
	formatter hermod.Formatter
	mu        sync.Mutex
}

// NewRabbitMQQueueSink creates a new RabbitMQ Queue sink.
func NewRabbitMQQueueSink(url string, queueName string, formatter hermod.Formatter) (*RabbitMQQueueSink, error) {
	return &RabbitMQQueueSink{
		url:       url,
		queue:     queueName,
		formatter: formatter,
	}, nil
}

func (s *RabbitMQQueueSink) ensureConnected(ctx context.Context) error {
	if s.url == "" {
		return errors.New("rabbitmq sink url is not configured")
	}
	if !strings.HasPrefix(s.url, "amqp://") && !strings.HasPrefix(s.url, "amqps://") {
		return errors.New("rabbitmq sink url must start with 'amqp://' or 'amqps://'")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.conn != nil && !s.conn.IsClosed() && s.channel != nil {
		return nil
	}

	conn, err := amqp.Dial(s.url)
	if err != nil {
		return fmt.Errorf("failed to connect to RabbitMQ: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return fmt.Errorf("failed to open a channel: %w", err)
	}

	_, err = ch.QueueDeclare(
		s.queue, // name
		true,    // durable
		false,   // delete when unused
		false,   // exclusive
		false,   // no-wait
		nil,     // arguments
	)
	if err != nil {
		ch.Close()
		conn.Close()
		return fmt.Errorf("failed to declare a queue: %w", err)
	}

	s.conn = conn
	s.channel = ch
	return nil
}

// Write sends a message to RabbitMQ Queue.
func (s *RabbitMQQueueSink) Write(ctx context.Context, msg hermod.Message) error {
	if msg == nil {
		return nil
	}
	if err := s.ensureConnected(ctx); err != nil {
		return err
	}

	s.mu.Lock()
	ch := s.channel
	s.mu.Unlock()

	if ch == nil {
		return fmt.Errorf("rabbitmq channel not connected")
	}

	var data []byte
	var err error

	if s.formatter != nil {
		data, err = s.formatter.Format(msg)
	} else {
		// Fallback to Payload-only if no formatter provided
		data = msg.Payload()
	}

	if err != nil {
		return fmt.Errorf("failed to format message: %w", err)
	}

	err = ch.PublishWithContext(ctx,
		"",      // exchange
		s.queue, // routing key
		false,   // mandatory
		false,   // immediate
		amqp.Publishing{
			ContentType: "application/json",
			Body:        data,
		})
	if err != nil {
		return fmt.Errorf("failed to publish message to RabbitMQ: %w", err)
	}

	return nil
}

// Ping checks if the RabbitMQ connection is alive.
func (s *RabbitMQQueueSink) Ping(ctx context.Context) error {
	return s.ensureConnected(ctx)
}

// Close closes the RabbitMQ channel and connection.
func (s *RabbitMQQueueSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.channel != nil {
		s.channel.Close()
		s.channel = nil
	}
	if s.conn != nil {
		s.conn.Close()
		s.conn = nil
	}
	return nil
}
