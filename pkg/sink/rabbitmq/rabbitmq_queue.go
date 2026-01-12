package rabbitmq

import (
	"context"
	"encoding/json"
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/user/hermod"
)

// RabbitMQQueueSink implements the hermod.Sink interface for RabbitMQ Queue.
type RabbitMQQueueSink struct {
	conn      *amqp.Connection
	channel   *amqp.Channel
	queue     string
	formatter hermod.Formatter
}

// NewRabbitMQQueueSink creates a new RabbitMQ Queue sink.
func NewRabbitMQQueueSink(url string, queueName string, formatter hermod.Formatter) (*RabbitMQQueueSink, error) {
	conn, err := amqp.Dial(url)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to RabbitMQ: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to open a channel: %w", err)
	}

	_, err = ch.QueueDeclare(
		queueName, // name
		true,      // durable
		false,     // delete when unused
		false,     // exclusive
		false,     // no-wait
		nil,       // arguments
	)
	if err != nil {
		ch.Close()
		conn.Close()
		return nil, fmt.Errorf("failed to declare a queue: %w", err)
	}

	return &RabbitMQQueueSink{
		conn:      conn,
		channel:   ch,
		queue:     queueName,
		formatter: formatter,
	}, nil
}

// Write sends a message to RabbitMQ Queue.
func (s *RabbitMQQueueSink) Write(ctx context.Context, msg hermod.Message) error {
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

	err = s.channel.PublishWithContext(ctx,
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
	if s.conn == nil || s.conn.IsClosed() {
		return fmt.Errorf("rabbitmq not connected")
	}
	return nil
}

// Close closes the RabbitMQ channel and connection.
func (s *RabbitMQQueueSink) Close() error {
	if s.channel != nil {
		s.channel.Close()
	}
	if s.conn != nil {
		s.conn.Close()
	}
	return nil
}
