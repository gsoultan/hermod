package kafka

import (
	"context"
	"fmt"
	"time"

	"github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/sasl/plain"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

type KafkaSource struct {
	reader    *kafka.Reader
	transport *kafka.Transport
}

func NewKafkaSource(brokers []string, topic, groupID string, username, password string) *KafkaSource {
	var transport *kafka.Transport
	var dialer *kafka.Dialer

	if username != "" {
		mechanism := plain.Mechanism{
			Username: username,
			Password: password,
		}
		transport = &kafka.Transport{
			SASL: mechanism,
		}
		dialer = &kafka.Dialer{
			Timeout:   10 * time.Second,
			DualStack: true,
		}
	} else {
		dialer = &kafka.Dialer{
			Timeout:   10 * time.Second,
			DualStack: true,
		}
	}

	return &KafkaSource{
		reader: kafka.NewReader(kafka.ReaderConfig{
			Brokers: brokers,
			Topic:   topic,
			GroupID: groupID,
			Dialer:  dialer,
		}),
		transport: transport,
	}
}

func (s *KafkaSource) Read(ctx context.Context) (hermod.Message, error) {
	m, err := s.reader.ReadMessage(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read message from kafka: %w", err)
	}

	msg := message.AcquireMessage()
	msg.SetID(string(m.Key))
	msg.SetPayload(m.Value)
	msg.SetMetadata("kafka_topic", m.Topic)
	msg.SetMetadata("kafka_partition", fmt.Sprintf("%d", m.Partition))
	msg.SetMetadata("kafka_offset", fmt.Sprintf("%d", m.Offset))

	return msg, nil
}

func (s *KafkaSource) Ack(ctx context.Context, msg hermod.Message) error {
	// kafka-go reader with GroupID automatically commits offsets on ReadMessage
	// unless explicitly disabled. For a more robust CDC-like behavior,
	// we might want FetchMessage + CommitMessages.
	return nil
}

func (s *KafkaSource) Ping(ctx context.Context) error {
	// Similar to KafkaSink Ping
	return nil
}

func (s *KafkaSource) Close() error {
	return s.reader.Close()
}
