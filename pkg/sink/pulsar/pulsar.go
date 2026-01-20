package pulsar

import (
	"context"
	"fmt"
	"sync"

	"github.com/apache/pulsar-client-go/pulsar"
	"github.com/user/hermod"
)

type PulsarSink struct {
	client    pulsar.Client
	producer  pulsar.Producer
	formatter hermod.Formatter
	url       string
	topic     string
	token     string
	mu        sync.Mutex
}

func NewPulsarSink(url string, topic string, token string, formatter hermod.Formatter) (*PulsarSink, error) {
	return &PulsarSink{
		url:       url,
		topic:     topic,
		token:     token,
		formatter: formatter,
	}, nil
}

func (s *PulsarSink) ensureConnected(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.client != nil && s.producer != nil {
		return nil
	}

	opts := pulsar.ClientOptions{
		URL: s.url,
	}
	if s.token != "" {
		opts.Authentication = pulsar.NewAuthenticationToken(s.token)
	}

	client, err := pulsar.NewClient(opts)
	if err != nil {
		return fmt.Errorf("failed to create pulsar client: %w", err)
	}

	producer, err := client.CreateProducer(pulsar.ProducerOptions{
		Topic: s.topic,
	})
	if err != nil {
		client.Close()
		return fmt.Errorf("failed to create pulsar producer: %w", err)
	}

	s.client = client
	s.producer = producer
	return nil
}

func (s *PulsarSink) Write(ctx context.Context, msg hermod.Message) error {
	if msg == nil {
		return nil
	}
	if err := s.ensureConnected(ctx); err != nil {
		return err
	}

	var data []byte
	var err error

	if s.formatter != nil {
		data, err = s.formatter.Format(msg)
	} else {
		data = msg.Payload()
	}

	if err != nil {
		return fmt.Errorf("failed to format message: %w", err)
	}

	_, err = s.producer.Send(ctx, &pulsar.ProducerMessage{
		Payload: data,
		Key:     msg.ID(),
	})
	if err != nil {
		return fmt.Errorf("failed to send message to pulsar: %w", err)
	}

	return nil
}

func (s *PulsarSink) Ping(ctx context.Context) error {
	return s.ensureConnected(ctx)
}

func (s *PulsarSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.producer != nil {
		s.producer.Close()
	}
	if s.client != nil {
		s.client.Close()
	}
	return nil
}
