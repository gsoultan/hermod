package pulsar

import (
	"context"
	"fmt"

	"github.com/apache/pulsar-client-go/pulsar"
	"github.com/user/hermod"
)

type PulsarSink struct {
	client    pulsar.Client
	producer  pulsar.Producer
	formatter hermod.Formatter
}

func NewPulsarSink(url string, topic string, token string, formatter hermod.Formatter) (*PulsarSink, error) {
	opts := pulsar.ClientOptions{
		URL: url,
	}
	if token != "" {
		opts.Authentication = pulsar.NewAuthenticationToken(token)
	}
	client, err := pulsar.NewClient(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to create pulsar client: %w", err)
	}

	producer, err := client.CreateProducer(pulsar.ProducerOptions{
		Topic: topic,
	})
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("failed to create pulsar producer: %w", err)
	}

	return &PulsarSink{
		client:    client,
		producer:  producer,
		formatter: formatter,
	}, nil
}

func (s *PulsarSink) Write(ctx context.Context, msg hermod.Message) error {
	data, err := s.formatter.Format(msg)
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
	// Send an empty message with properties to check connectivity if Send is too heavy
	// Actually Pulsar client doesn't have a simple Ping.
	// We can check if the producer is ready by trying to flush or just use a simple check.
	// For now, let's just return nil as the client handles reconnection.
	// But we can check if the client is not nil.
	if s.client == nil || s.producer == nil {
		return fmt.Errorf("pulsar client or producer is not initialized")
	}
	return nil
}

func (s *PulsarSink) Close() error {
	s.producer.Close()
	s.client.Close()
	return nil
}
