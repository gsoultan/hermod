package kafka

import (
	"context"
	"fmt"
	"time"

	"github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/sasl/plain"
	"github.com/user/hermod"
)

type KafkaSink struct {
	writer    *kafka.Writer
	transport *kafka.Transport
	formatter hermod.Formatter
}

func NewKafkaSink(brokers []string, topic string, username, password string, formatter hermod.Formatter) *KafkaSink {
	var transport *kafka.Transport
	if username != "" {
		transport = &kafka.Transport{
			SASL: plain.Mechanism{
				Username: username,
				Password: password,
			},
		}
	}

	return &KafkaSink{
		writer: &kafka.Writer{
			Addr:                   kafka.TCP(brokers...),
			Topic:                  topic,
			Balancer:               &kafka.LeastBytes{},
			AllowAutoTopicCreation: true,
			Transport:              transport,
		},
		transport: transport,
		formatter: formatter,
	}
}

func (s *KafkaSink) Write(ctx context.Context, msg hermod.Message) error {
	data, err := s.formatter.Format(msg)
	if err != nil {
		return fmt.Errorf("failed to format message: %w", err)
	}

	err = s.writer.WriteMessages(ctx, kafka.Message{
		Key:   []byte(msg.ID()),
		Value: data,
	})
	if err != nil {
		return fmt.Errorf("failed to write message to kafka: %w", err)
	}

	return nil
}

func (s *KafkaSink) Ping(ctx context.Context) error {
	client := &kafka.Client{
		Addr:      s.writer.Addr,
		Transport: s.transport,
		Timeout:   10 * time.Second,
	}
	_, err := client.Metadata(ctx, &kafka.MetadataRequest{
		Topics: []string{s.writer.Topic},
	})
	if err != nil {
		return fmt.Errorf("failed to ping kafka: %w", err)
	}
	return nil
}

func (s *KafkaSink) Close() error {
	return s.writer.Close()
}
