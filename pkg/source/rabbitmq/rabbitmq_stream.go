package rabbitmq

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/rabbitmq/rabbitmq-stream-go-client/pkg/amqp"
	"github.com/rabbitmq/rabbitmq-stream-go-client/pkg/stream"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

type RabbitMQStreamSource struct {
	env          *stream.Environment
	consumer     *stream.Consumer
	url          string
	stream       string
	consumerName string
	messages     chan hermod.Message
	errs         chan error
	mu           sync.Mutex
	lastOffset   int64
}

func NewRabbitMQStreamSource(url string, streamName string, consumerName string) (*RabbitMQStreamSource, error) {
	return &RabbitMQStreamSource{
		url:          url,
		stream:       streamName,
		consumerName: consumerName,
		messages:     make(chan hermod.Message, 100),
		errs:         make(chan error, 1),
		lastOffset:   -1,
	}, nil
}

func (s *RabbitMQStreamSource) ensureConnected() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.env != nil && !s.env.IsClosed() && s.consumer != nil {
		return nil
	}

	env, err := stream.NewEnvironment(stream.NewEnvironmentOptions().SetUri(s.url))
	if err != nil {
		return fmt.Errorf("failed to create RabbitMQ stream environment: %w", err)
	}

	handleMessages := func(consumerContext stream.ConsumerContext, amqpMsg *amqp.Message) {
		if amqpMsg == nil {
			return
		}

		hmsg := message.AcquireMessage()
		data := amqpMsg.GetData()
		hmsg.SetPayload(data)

		if consumerContext.Consumer != nil {
			hmsg.SetMetadata("rabbitmq_stream_offset", strconv.FormatInt(consumerContext.Consumer.GetOffset(), 10))
		}

		// Try to unmarshal JSON into Data() for dynamic structure
		var jsonData map[string]any
		if err := json.Unmarshal(data, &jsonData); err == nil {
			for k, v := range jsonData {
				hmsg.SetData(k, v)
			}
		} else {
			hmsg.SetAfter(data) // Fallback for non-JSON
		}

		s.messages <- hmsg
	}

	opts := stream.NewConsumerOptions().SetConsumerName(s.consumerName)
	if s.lastOffset >= 0 {
		opts.SetOffset(stream.OffsetSpecification{}.Offset(s.lastOffset + 1))
	} else {
		// If we have a consumer name, RabbitMQ will try to use the stored offset.
		// If not, we start from the last message.
		opts.SetOffset(stream.OffsetSpecification{}.Last())
	}

	consumer, err := env.NewConsumer(s.stream, handleMessages, opts)
	if err != nil {
		env.Close()
		return fmt.Errorf("failed to create RabbitMQ stream consumer: %w", err)
	}

	if s.consumer != nil {
		s.consumer.Close()
	}
	if s.env != nil {
		s.env.Close()
	}

	s.env = env
	s.consumer = consumer
	return nil
}

func (s *RabbitMQStreamSource) Read(ctx context.Context) (hermod.Message, error) {
	if err := s.ensureConnected(); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case msg := <-s.messages:
		return msg, nil
	case err := <-s.errs:
		s.mu.Lock()
		if s.env != nil {
			s.env.Close()
			s.env = nil
		}
		s.mu.Unlock()
		return nil, err
	}
}

func (s *RabbitMQStreamSource) Ack(ctx context.Context, msg hermod.Message) error {
	if msg == nil {
		return nil
	}
	offsetStr := msg.Metadata()["rabbitmq_stream_offset"]
	if offsetStr == "" {
		return nil
	}

	offset, err := strconv.ParseInt(offsetStr, 10, 64)
	if err != nil {
		return fmt.Errorf("failed to parse rabbitmq stream offset: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.lastOffset = offset
	if s.consumer != nil {
		return s.consumer.StoreCustomOffset(offset)
	}
	return nil
}

func (s *RabbitMQStreamSource) Sample(ctx context.Context, table string) (hermod.Message, error) {
	env, err := stream.NewEnvironment(stream.NewEnvironmentOptions().SetUri(s.url))
	if err != nil {
		return nil, fmt.Errorf("sample connection failed: %w", err)
	}
	defer env.Close()

	var msg hermod.Message
	done := make(chan struct{})

	handleMessages := func(consumerContext stream.ConsumerContext, amqpMsg *amqp.Message) {
		if amqpMsg == nil {
			return
		}

		hmsg := message.AcquireMessage()
		data := amqpMsg.GetData()
		hmsg.SetPayload(data)

		var jsonData map[string]any
		if err := json.Unmarshal(data, &jsonData); err == nil {
			for k, v := range jsonData {
				hmsg.SetData(k, v)
			}
		} else {
			hmsg.SetAfter(data)
		}
		msg = hmsg
		close(done)
	}

	consumer, err := env.NewConsumer(s.stream, handleMessages, stream.NewConsumerOptions().SetOffset(stream.OffsetSpecification{}.Last()))
	if err != nil {
		return nil, fmt.Errorf("sample consumer failed: %w", err)
	}
	defer consumer.Close()

	select {
	case <-done:
		return msg, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(2 * time.Second):
		return nil, fmt.Errorf("sample timeout: no messages found")
	}
}

func (s *RabbitMQStreamSource) Ping(ctx context.Context) error {
	s.mu.Lock()
	env := s.env
	s.mu.Unlock()

	if env != nil && !env.IsClosed() {
		return nil
	}

	// Just try to create environment and close it immediately
	env, err := stream.NewEnvironment(stream.NewEnvironmentOptions().SetUri(s.url))
	if err != nil {
		return fmt.Errorf("failed to dial RabbitMQ Stream for ping: %w", err)
	}
	env.Close()
	return nil
}

func (s *RabbitMQStreamSource) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.consumer != nil {
		s.consumer.Close()
		s.consumer = nil
	}
	if s.env != nil {
		s.env.Close()
		s.env = nil
	}
	return nil
}
