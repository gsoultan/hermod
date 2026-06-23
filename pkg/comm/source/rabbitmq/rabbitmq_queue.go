package rabbitmq

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/comm/message"
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

// lastConsumedCache stores the most recently consumed raw payload per
// url+queue. When a workflow is actively consuming a queue (auto-ack=false,
// no prefetch limit) every available message is held in that consumer's
// unacked buffer, leaving the queue empty for a passive `Basic.Get`. Sampling
// therefore falls back to the latest message that was actually consumed so the
// UI never shows an empty sample while data is flowing.
var lastConsumedCache sync.Map // map[string][]byte

func lastConsumedKey(url, queue string) string {
	return url + "|" + queue
}

func storeLastConsumed(url, queue string, body []byte) {
	lastConsumedCache.Store(lastConsumedKey(url, queue), bytes.Clone(body))
}

func loadLastConsumed(url, queue string) ([]byte, bool) {
	v, ok := lastConsumedCache.Load(lastConsumedKey(url, queue))
	if !ok {
		return nil, false
	}
	body, ok := v.([]byte)
	return body, ok
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
	if s.url == "" {
		return errors.New("rabbitmq source url is not configured")
	}
	if !strings.HasPrefix(s.url, "amqp://") && !strings.HasPrefix(s.url, "amqps://") {
		return errors.New("rabbitmq source url must start with 'amqp://' or 'amqps://'")
	}
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
				s.errs <- errors.New("rabbitmq channel closed")
				return
			}
			// Remember the latest consumed payload so passive sampling can
			// surface it even when the queue is drained by this consumer.
			storeLastConsumed(s.url, s.queue, d.Body)

			hmsg := message.AcquireMessage()
			hmsg.SetPayload(d.Body)

			// Try to unmarshal JSON into Data() for dynamic structure
			var jsonData map[string]any
			if err := json.Unmarshal(d.Body, &jsonData); err == nil {
				for k, v := range jsonData {
					hmsg.SetData(k, v)
				}
			} else if fixed := message.TryFixJSON(d.Body); fixed != nil {
				if err := json.Unmarshal(fixed, &jsonData); err == nil {
					for k, v := range jsonData {
						hmsg.SetData(k, v)
					}
				} else {
					hmsg.SetAfter(d.Body) // Fallback for non-JSON
				}
			} else {
				hmsg.SetAfter(d.Body) // Fallback for non-JSON
			}

			// Every message needs a unique, stable ID for tracing and
			// idempotency. Prefer the AMQP message-id when the publisher set
			// one; otherwise generate a UUID (mirroring the other sources).
			// Without this, trace steps are keyed by an empty message ID and
			// never surface in the message-trace API/UI.
			if d.MessageId != "" {
				hmsg.SetID(d.MessageId)
			} else {
				hmsg.SetID(uuid.NewString())
			}
			// Store delivery tag in metadata for Ack
			hmsg.SetMetadata("delivery_tag", strconv.FormatUint(d.DeliveryTag, 10))
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
		s.mu.Lock()
		if s.conn != nil {
			s.conn.Close() // Force reconnection on next Read
			s.conn = nil
		}
		s.mu.Unlock()
		return nil, err
	}
}

func (s *RabbitMQQueueSource) Ack(ctx context.Context, msg hermod.Message) error {
	if msg == nil {
		return nil
	}
	tagStr := msg.Metadata()["delivery_tag"]
	if tagStr == "" {
		return errors.New("missing delivery_tag in message metadata")
	}

	var tag uint64
	if _, err := fmt.Sscanf(tagStr, "%d", &tag); err != nil {
		return fmt.Errorf("invalid delivery_tag: %w", err)
	}

	s.mu.Lock()
	ch := s.channel
	s.mu.Unlock()

	if ch == nil {
		return errors.New("rabbitmq channel not connected")
	}

	return ch.Ack(tag, false)
}

func (s *RabbitMQQueueSource) Sample(ctx context.Context, table string) (hermod.Message, error) {
	conn, err := amqp.Dial(s.url)
	if err != nil {
		return nil, fmt.Errorf("sample connection failed: %w", err)
	}
	defer conn.Close()

	ch, err := conn.Channel()
	if err != nil {
		return nil, fmt.Errorf("sample channel failed: %w", err)
	}
	defer ch.Close()

	d, ok, err := ch.Get(s.queue, false) // false for auto-ack: we don't want to consume the message, but RabbitMQ doesn't have a pure "peek" without consumption.
	// Actually, if we use Get with autoAck=false, the message is redelivered if we don't Ack it and close the channel.
	if err != nil {
		return nil, fmt.Errorf("sample get failed: %w", err)
	}
	if !ok {
		// The queue is empty for a passive Get. This commonly happens when a
		// running workflow consumer holds every available message in its
		// unacked buffer. Fall back to the latest message we actually consumed.
		return s.sampleFromLastConsumed()
	}

	// We don't Ack, so it should stay in the queue when we close the channel.
	// But to be extra safe, we could Nack it.
	defer ch.Nack(d.DeliveryTag, false, true)

	return buildSampleMessage(d.Body, d.MessageId), nil
}

// sampleFromLastConsumed returns a message built from the most recently
// consumed payload for this queue, if one has been observed.
func (s *RabbitMQQueueSource) sampleFromLastConsumed() (hermod.Message, error) {
	body, ok := loadLastConsumed(s.url, s.queue)
	if !ok {
		return nil, errors.New("queue is empty")
	}
	return buildSampleMessage(body, ""), nil
}

// buildSampleMessage converts a raw payload into a hermod.Message, decoding
// JSON into the message data when possible.
func buildSampleMessage(body []byte, messageID string) hermod.Message {
	hmsg := message.AcquireMessage()
	hmsg.SetPayload(body)

	var jsonData map[string]any
	if err := json.Unmarshal(body, &jsonData); err == nil {
		for k, v := range jsonData {
			hmsg.SetData(k, v)
		}
	} else {
		hmsg.SetAfter(body)
	}

	if messageID != "" {
		hmsg.SetID(messageID)
	}

	return hmsg
}

func (s *RabbitMQQueueSource) Ping(ctx context.Context) error {
	if s.url == "" {
		return errors.New("rabbitmq source url is not configured")
	}
	if !strings.HasPrefix(s.url, "amqp://") && !strings.HasPrefix(s.url, "amqps://") {
		return errors.New("rabbitmq source url must start with 'amqp://' or 'amqps://'")
	}
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
