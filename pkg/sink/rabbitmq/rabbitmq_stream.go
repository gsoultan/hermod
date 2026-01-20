package rabbitmq

import (
	"context"
	"fmt"
	"sync"

	"github.com/rabbitmq/rabbitmq-stream-go-client/pkg/amqp"
	"github.com/rabbitmq/rabbitmq-stream-go-client/pkg/stream"
	"github.com/user/hermod"
)

// RabbitMQStreamSink implements the hermod.Sink interface for RabbitMQ Stream.
type RabbitMQStreamSink struct {
	env       *stream.Environment
	producer  *stream.Producer
	url       string
	stream    string
	formatter hermod.Formatter
	mu        sync.Mutex
}

// NewRabbitMQStreamSink creates a new RabbitMQ Stream sink.
func NewRabbitMQStreamSink(url string, streamName string, formatter hermod.Formatter) (*RabbitMQStreamSink, error) {
	return &RabbitMQStreamSink{
		url:       url,
		stream:    streamName,
		formatter: formatter,
	}, nil
}

func (s *RabbitMQStreamSink) ensureConnected(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.env != nil && !s.env.IsClosed() && s.producer != nil {
		return nil
	}

	env, err := stream.NewEnvironment(stream.NewEnvironmentOptions().SetUri(s.url))
	if err != nil {
		return fmt.Errorf("failed to create RabbitMQ stream environment: %w", err)
	}

	producer, err := env.NewProducer(s.stream, nil)
	if err != nil {
		env.Close()
		return fmt.Errorf("failed to create RabbitMQ stream producer: %w", err)
	}

	s.env = env
	s.producer = producer
	return nil
}

// Write sends a message to RabbitMQ Stream.
func (s *RabbitMQStreamSink) Write(ctx context.Context, msg hermod.Message) error {
	if msg == nil {
		return nil
	}
	if err := s.ensureConnected(ctx); err != nil {
		return err
	}

	s.mu.Lock()
	producer := s.producer
	s.mu.Unlock()

	if producer == nil {
		return fmt.Errorf("rabbitmq stream producer not connected")
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

	err = producer.Send(amqp.NewMessage(data))
	if err != nil {
		return fmt.Errorf("failed to send message to RabbitMQ Stream: %w", err)
	}

	return nil
}

// Ping checks if the RabbitMQ connection is alive.
func (s *RabbitMQStreamSink) Ping(ctx context.Context) error {
	return s.ensureConnected(ctx)
}

// Close closes the RabbitMQ stream producer and environment.
func (s *RabbitMQStreamSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.producer != nil {
		s.producer.Close()
		s.producer = nil
	}
	if s.env != nil {
		err := s.env.Close()
		s.env = nil
		return err
	}
	return nil
}
