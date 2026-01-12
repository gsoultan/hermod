package rabbitmq

import (
	"context"
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

type RabbitMQQueueSource struct {
	conn     *amqp.Connection
	channel  *amqp.Channel
	msgs     <-chan amqp.Delivery
	queue    string
	messages chan hermod.Message
	errs     chan error
	cancel   context.CancelFunc
}

func NewRabbitMQQueueSource(url string, queueName string) (*RabbitMQQueueSource, error) {
	conn, err := amqp.Dial(url)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to RabbitMQ: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to open a channel: %w", err)
	}

	q, err := ch.QueueDeclare(
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

	msgs, err := ch.Consume(
		q.Name, // queue
		"",     // consumer
		true,   // auto-ack
		false,  // exclusive
		false,  // no-local
		false,  // no-wait
		nil,    // args
	)
	if err != nil {
		ch.Close()
		conn.Close()
		return nil, fmt.Errorf("failed to register a consumer: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	s := &RabbitMQQueueSource{
		conn:     conn,
		channel:  ch,
		msgs:     msgs,
		queue:    queueName,
		messages: make(chan hermod.Message, 100),
		errs:     make(chan error, 1),
		cancel:   cancel,
	}

	go s.run(ctx)

	return s, nil
}

func (s *RabbitMQQueueSource) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case d, ok := <-s.msgs:
			if !ok {
				s.errs <- fmt.Errorf("rabbitmq channel closed")
				return
			}
			hmsg := message.AcquireMessage()
			hmsg.SetPayload(d.Body)
			// Optionally set other fields if available in headers
			if d.MessageId != "" {
				hmsg.SetID(d.MessageId)
			}
			s.messages <- hmsg
		}
	}
}

func (s *RabbitMQQueueSource) Read(ctx context.Context) (hermod.Message, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case msg := <-s.messages:
		return msg, nil
	case err := <-s.errs:
		return nil, err
	}
}

func (s *RabbitMQQueueSource) Ack(ctx context.Context, msg hermod.Message) error {
	// If we used auto-ack = false, we would ack here.
	// For simplicity, we currently use auto-ack = true in NewRabbitMQQueueSource.
	return nil
}

func (s *RabbitMQQueueSource) Ping(ctx context.Context) error {
	if s.conn == nil || s.conn.IsClosed() {
		return fmt.Errorf("rabbitmq not connected")
	}
	return nil
}

func (s *RabbitMQQueueSource) Close() error {
	s.cancel()
	if s.channel != nil {
		s.channel.Close()
	}
	if s.conn != nil {
		s.conn.Close()
	}
	return nil
}
