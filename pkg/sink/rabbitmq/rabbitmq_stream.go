package rabbitmq

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rabbitmq/rabbitmq-stream-go-client/pkg/amqp"
	"github.com/rabbitmq/rabbitmq-stream-go-client/pkg/stream"
	"github.com/user/hermod"
)

// RabbitMQStreamSink implements the hermod.Sink interface for RabbitMQ Stream.
type RabbitMQStreamSink struct {
	env       *stream.Environment
	producer  *stream.Producer
	stream    string
	formatter hermod.Formatter
}

// NewRabbitMQStreamSink creates a new RabbitMQ Stream sink.
func NewRabbitMQStreamSink(url string, streamName string, formatter hermod.Formatter) (*RabbitMQStreamSink, error) {
	env, err := stream.NewEnvironment(stream.NewEnvironmentOptions().SetUri(url))
	if err != nil {
		return nil, fmt.Errorf("failed to create RabbitMQ stream environment: %w", err)
	}

	producer, err := env.NewProducer(streamName, nil)
	if err != nil {
		env.Close()
		return nil, fmt.Errorf("failed to create RabbitMQ stream producer: %w", err)
	}

	return &RabbitMQStreamSink{
		env:       env,
		producer:  producer,
		stream:    streamName,
		formatter: formatter,
	}, nil
}

// Write sends a message to RabbitMQ Stream.
func (s *RabbitMQStreamSink) Write(ctx context.Context, msg hermod.Message) error {
	var data []byte
	var err error

	if s.formatter != nil {
		data, err = s.formatter.Format(msg)
	} else {
		data, err = json.Marshal(map[string]interface{}{
			"id":        msg.ID(),
			"operation": msg.Operation(),
			"table":     msg.Table(),
			"schema":    msg.Schema(),
			"before":    json.RawMessage(msg.Before()),
			"after":     json.RawMessage(msg.After()),
			"metadata":  msg.Metadata(),
		})
	}

	if err != nil {
		return fmt.Errorf("failed to format message: %w", err)
	}

	err = s.producer.Send(amqp.NewMessage(data))
	if err != nil {
		return fmt.Errorf("failed to send message to RabbitMQ Stream: %w", err)
	}

	return nil
}

// Ping checks if the RabbitMQ connection is alive.
func (s *RabbitMQStreamSink) Ping(ctx context.Context) error {
	if s.env == nil {
		return fmt.Errorf("rabbitmq environment is nil")
	}
	// The client doesn't have a direct Ping, but we can check if it's closed
	if s.env.IsClosed() {
		return fmt.Errorf("rabbitmq environment is closed")
	}
	return nil
}

// Close closes the RabbitMQ stream producer and environment.
func (s *RabbitMQStreamSink) Close() error {
	err := s.producer.Close()
	if err != nil {
		s.env.Close()
		return fmt.Errorf("failed to close RabbitMQ stream producer: %w", err)
	}
	return s.env.Close()
}
