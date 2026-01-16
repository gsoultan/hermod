package rabbitmq

import (
	"context"
	"encoding/json"
	"fmt"

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
}

func NewRabbitMQStreamSource(url string, streamName string, consumerName string) (*RabbitMQStreamSource, error) {
	return &RabbitMQStreamSource{
		url:          url,
		stream:       streamName,
		consumerName: consumerName,
		messages:     make(chan hermod.Message, 100),
		errs:         make(chan error, 1),
	}, nil
}

func (s *RabbitMQStreamSource) ensureConnected() error {
	if s.env != nil && !s.env.IsClosed() {
		return nil
	}

	env, err := stream.NewEnvironment(stream.NewEnvironmentOptions().SetUri(s.url))
	if err != nil {
		return fmt.Errorf("failed to create RabbitMQ stream environment: %w", err)
	}

	handleMessages := func(consumerContext stream.ConsumerContext, amqpMsg *amqp.Message) {
		hmsg := message.AcquireMessage()
		data := amqpMsg.GetData()
		hmsg.SetPayload(data)

		// Try to unmarshal JSON into Data() for dynamic structure
		var jsonData map[string]interface{}
		if err := json.Unmarshal(data, &jsonData); err == nil {
			for k, v := range jsonData {
				hmsg.SetData(k, v)
			}
		} else {
			hmsg.SetAfter(data) // Fallback for non-JSON
		}

		s.messages <- hmsg
	}

	consumer, err := env.NewConsumer(s.stream, handleMessages, stream.NewConsumerOptions().SetConsumerName(s.consumerName))
	if err != nil {
		env.Close()
		return fmt.Errorf("failed to create RabbitMQ stream consumer: %w", err)
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
		return nil, err
	}
}

func (s *RabbitMQStreamSource) Ack(ctx context.Context, msg hermod.Message) error {
	return nil
}

func (s *RabbitMQStreamSource) Ping(ctx context.Context) error {
	return s.ensureConnected()
}

func (s *RabbitMQStreamSource) Close() error {
	if s.consumer != nil {
		s.consumer.Close()
	}
	if s.env != nil {
		s.env.Close()
	}
	return nil
}
