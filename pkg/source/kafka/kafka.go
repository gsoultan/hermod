package kafka

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/sasl/plain"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

type KafkaSource struct {
	reader    *kafka.Reader
	transport *kafka.Transport
	brokers   []string
	topic     string
	username  string
	password  string
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
		brokers:   brokers,
		topic:     topic,
		username:  username,
		password:  password,
	}
}

func (s *KafkaSource) Read(ctx context.Context) (hermod.Message, error) {
	m, err := s.reader.FetchMessage(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch message from kafka: %w", err)
	}

	msg := message.AcquireMessage()
	msg.SetID(string(m.Key))
	msg.SetPayload(m.Value)

	// Try to unmarshal JSON into Data() for dynamic structure
	var jsonData map[string]any
	if err := json.Unmarshal(m.Value, &jsonData); err == nil {
		for k, v := range jsonData {
			msg.SetData(k, v)
		}
	} else {
		msg.SetAfter(m.Value) // Fallback for non-JSON
	}

	msg.SetMetadata("kafka_topic", m.Topic)
	msg.SetMetadata("kafka_partition", fmt.Sprintf("%d", m.Partition))
	msg.SetMetadata("kafka_offset", fmt.Sprintf("%d", m.Offset))

	return msg, nil
}

func (s *KafkaSource) Ack(ctx context.Context, msg hermod.Message) error {
	topic := msg.Metadata()["kafka_topic"]
	partitionStr := msg.Metadata()["kafka_partition"]
	offsetStr := msg.Metadata()["kafka_offset"]

	if topic == "" || partitionStr == "" || offsetStr == "" {
		return fmt.Errorf("missing kafka metadata in message")
	}

	var partition int
	var offset int64
	fmt.Sscanf(partitionStr, "%d", &partition)
	fmt.Sscanf(offsetStr, "%d", &offset)

	err := s.reader.CommitMessages(ctx, kafka.Message{
		Topic:     topic,
		Partition: partition,
		Offset:    offset,
	})
	if err != nil {
		return fmt.Errorf("failed to commit kafka offset: %w", err)
	}
	return nil
}

func (s *KafkaSource) IsReady(ctx context.Context) error {
	if err := s.Ping(ctx); err != nil {
		return fmt.Errorf("kafka connection failed: %w", err)
	}

	// Check if brokers are reachable and topic exists
	conn, err := s.reader.Config().Dialer.DialContext(ctx, "tcp", s.brokers[0])
	if err != nil {
		return fmt.Errorf("failed to dial kafka broker %s: %w", s.brokers[0], err)
	}
	defer conn.Close()

	partitions, err := conn.ReadPartitions(s.topic)
	if err != nil {
		return fmt.Errorf("failed to read partitions for topic '%s': %w. Ensure topic exists and user has permissions", s.topic, err)
	}

	if len(partitions) == 0 {
		return fmt.Errorf("kafka topic '%s' has no partitions", s.topic)
	}

	return nil
}

func (s *KafkaSource) Ping(ctx context.Context) error {
	// Try to dial first broker
	conn, err := s.reader.Config().Dialer.DialContext(ctx, "tcp", s.brokers[0])
	if err != nil {
		return fmt.Errorf("kafka ping failed for broker %s: %w", s.brokers[0], err)
	}
	conn.Close()
	return nil
}

func (s *KafkaSource) Sample(ctx context.Context, table string) (hermod.Message, error) {
	// Create a one-off reader with a random group ID to avoid affecting existing consumers
	sampler := NewKafkaSource(s.brokers, s.topic, "hermod-sampler-"+uuid.New().String(), s.username, s.password)
	defer sampler.Close()

	// We set a timeout to avoid blocking forever if the topic is empty
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	return sampler.Read(ctx)
}

func (s *KafkaSource) Close() error {
	return s.reader.Close()
}
