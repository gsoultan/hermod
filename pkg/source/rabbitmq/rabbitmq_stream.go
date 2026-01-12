package rabbitmq

import (
	"context"
	"fmt"

	"github.com/rabbitmq/rabbitmq-stream-go-client/pkg/amqp"
	"github.com/rabbitmq/rabbitmq-stream-go-client/pkg/stream"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

type RabbitMQStreamSource struct {
	env      *stream.Environment
	consumer *stream.Consumer
	stream   string
	messages chan hermod.Message
	errs     chan error
}

func NewRabbitMQStreamSource(url string, streamName string, consumerName string) (*RabbitMQStreamSource, error) {
	env, err := stream.NewEnvironment(stream.NewEnvironmentOptions().SetUri(url))
	if err != nil {
		return nil, fmt.Errorf("failed to create RabbitMQ stream environment: %w", err)
	}

	s := &RabbitMQStreamSource{
		env:      env,
		stream:   streamName,
		messages: make(chan hermod.Message, 100),
		errs:     make(chan error, 1),
	}

	handleMessages := func(consumerContext stream.ConsumerContext, amqpMsg *amqp.Message) {
		hmsg := message.AcquireMessage()
		hmsg.SetPayload(amqpMsg.GetData())
		s.messages <- hmsg
	}

	consumer, err := env.NewConsumer(streamName, handleMessages, stream.NewConsumerOptions().SetConsumerName(consumerName))
	if err != nil {
		env.Close()
		return nil, fmt.Errorf("failed to create RabbitMQ stream consumer: %w", err)
	}

	s.consumer = consumer
	return s, nil
}

func (s *RabbitMQStreamSource) Read(ctx context.Context) (hermod.Message, error) {
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
	if s.env == nil || s.env.IsClosed() {
		return fmt.Errorf("rabbitmq not connected")
	}
	return nil
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
