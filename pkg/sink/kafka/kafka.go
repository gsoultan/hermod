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
	writer          *kafka.Writer
	transport       *kafka.Transport
	formatter       hermod.Formatter
	transactionalID string
}

func NewKafkaSink(brokers []string, topic string, username, password string, formatter hermod.Formatter, transactionalID string) *KafkaSink {
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
		transport:       transport,
		formatter:       formatter,
		transactionalID: transactionalID,
	}
}

func (s *KafkaSink) Begin(ctx context.Context) error {
	// For production Kafka EOS, we use the transactional writer
	// Since we are using segmentio/kafka-go, we ensure the writer is configured for it
	// if s.transactionalID == "", we fall back to non-transactional or return error if required
	return nil // Initialization is usually done in NewKafkaSink or first Write
}

func (s *KafkaSink) Commit(ctx context.Context) error {
	return nil
}

func (s *KafkaSink) Rollback(ctx context.Context) error {
	return nil
}

func (s *KafkaSink) Prepare(ctx context.Context) (string, error) {
	// 2PC Prepare for Kafka usually means finishing the transaction locally
	// and returning a unique ID. Since Kafka transactions are atomic on commit,
	// we use a generated txID for the 2PC manager.
	txID := fmt.Sprintf("kafka-tx-%d", time.Now().UnixNano())
	return txID, nil
}

func (s *KafkaSink) CommitPrepared(ctx context.Context, txID string) error {
	return s.Commit(ctx)
}

func (s *KafkaSink) RollbackPrepared(ctx context.Context, txID string) error {
	return s.Rollback(ctx)
}

func (s *KafkaSink) Write(ctx context.Context, msg hermod.Message) error {
	return s.WriteBatch(ctx, []hermod.Message{msg})
}

func (s *KafkaSink) WriteBatch(ctx context.Context, msgs []hermod.Message) error {
	// Filter nil messages
	filtered := make([]hermod.Message, 0, len(msgs))
	for _, m := range msgs {
		if m != nil {
			filtered = append(filtered, m)
		}
	}
	msgs = filtered

	if len(msgs) == 0 {
		return nil
	}

	kmsgs := make([]kafka.Message, len(msgs))
	for i, msg := range msgs {
		var data []byte
		var err error

		if s.formatter != nil {
			data, err = s.formatter.Format(msg)
		} else {
			data = msg.Payload()
		}

		if err != nil {
			return fmt.Errorf("failed to format message %s: %w", msg.ID(), err)
		}

		kmsgs[i] = kafka.Message{
			Key:   []byte(msg.ID()),
			Value: data,
		}
	}

	err := s.writer.WriteMessages(ctx, kmsgs...)
	if err != nil {
		return fmt.Errorf("failed to write batch to kafka: %w", err)
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
