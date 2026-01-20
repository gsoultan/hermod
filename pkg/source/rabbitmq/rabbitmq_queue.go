package rabbitmq

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

type RabbitMQQueueSource struct {
	conn     *amqp.Connection
	channel  *amqp.Channel
	msgs     <-chan amqp.Delivery
	url      string
	queue    string
	messages chan hermod.Message
	errs     chan error
	cancel   context.CancelFunc
	mu       sync.Mutex
}

func NewRabbitMQQueueSource(url string, queueName string) (*RabbitMQQueueSource, error) {
	return &RabbitMQQueueSource{
		url:      url,
		queue:    queueName,
		messages: make(chan hermod.Message, 100),
		errs:     make(chan error, 1),
	}, nil
}

func (s *RabbitMQQueueSource) ensureConnected() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.conn != nil && !s.conn.IsClosed() {
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

	q, err := ch.QueueDeclare(
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

	msgs, err := ch.Consume(
		q.Name, // queue
		"",     // consumer
		false,  // auto-ack
		false,  // exclusive
		false,  // no-local
		false,  // no-wait
		nil,    // args
	)
	if err != nil {
		ch.Close()
		conn.Close()
		return fmt.Errorf("failed to register a consumer: %w", err)
	}

	if s.cancel != nil {
		s.cancel()
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.conn = conn
	s.channel = ch
	s.msgs = msgs
	s.cancel = cancel

	go s.run(ctx, msgs)

	return nil
}

func (s *RabbitMQQueueSource) run(ctx context.Context, msgs <-chan amqp.Delivery) {
	for {
		select {
		case <-ctx.Done():
			return
		case d, ok := <-msgs:
			if !ok {
				s.errs <- fmt.Errorf("rabbitmq channel closed")
				return
			}
			hmsg := message.AcquireMessage()
			hmsg.SetPayload(d.Body)

			// Try to unmarshal JSON into Data() for dynamic structure
			var jsonData map[string]interface{}
			if err := json.Unmarshal(d.Body, &jsonData); err == nil {
				for k, v := range jsonData {
					hmsg.SetData(k, v)
				}
			} else {
				hmsg.SetAfter(d.Body) // Fallback for non-JSON
			}

			// Optionally set other fields if available in headers
			if d.MessageId != "" {
				hmsg.SetID(d.MessageId)
			}
			// Store delivery tag in metadata for Ack
			hmsg.SetMetadata("delivery_tag", fmt.Sprintf("%d", d.DeliveryTag))
			s.messages <- hmsg
		}
	}
}

func (s *RabbitMQQueueSource) Read(ctx context.Context) (hermod.Message, error) {
	if err := s.ensureConnected(); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case msg := <-s.messages:
		return msg, nil
	case err := <-s.errs:
		// Attempt to reconnect on error
		if err.Error() == "rabbitmq channel closed" {
			s.mu.Lock()
			if s.conn != nil {
				s.conn.Close() // Force reconnection on next Read
			}
			s.mu.Unlock()
		}
		return nil, err
	}
}

func (s *RabbitMQQueueSource) Ack(ctx context.Context, msg hermod.Message) error {
	if msg == nil {
		return nil
	}
	tagStr := msg.Metadata()["delivery_tag"]
	if tagStr == "" {
		return fmt.Errorf("missing delivery_tag in message metadata")
	}

	var tag uint64
	if _, err := fmt.Sscanf(tagStr, "%d", &tag); err != nil {
		return fmt.Errorf("invalid delivery_tag: %w", err)
	}

	s.mu.Lock()
	ch := s.channel
	s.mu.Unlock()

	if ch == nil {
		return fmt.Errorf("rabbitmq channel not connected")
	}

	return ch.Ack(tag, false)
}

func (s *RabbitMQQueueSource) Ping(ctx context.Context) error {
	s.mu.Lock()
	conn := s.conn
	s.mu.Unlock()

	if conn != nil && !conn.IsClosed() {
		return nil
	}

	// Just try to dial, don't open channels or declare queues
	conn, err := amqp.Dial(s.url)
	if err != nil {
		return fmt.Errorf("failed to dial RabbitMQ for ping: %w", err)
	}
	conn.Close()
	return nil
}

func (s *RabbitMQQueueSource) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
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
