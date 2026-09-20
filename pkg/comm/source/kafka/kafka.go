package kafka

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/sasl/plain"
)

type KafkaSource struct {
	reader    *kafka.Reader
	transport *kafka.Transport
	brokers   []string
	topic     string
	username  string
	password  string
	// decoder, when set, reads Confluent-framed records. Nil means the
	// historical behaviour: try JSON, fall back to raw bytes.
	decoder recordDecoder
	mu      sync.Mutex
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

// recordDecoder reads a Confluent-framed record. It is an interface so the
// source does not depend on the registry client's construction.
type recordDecoder interface {
	Decode(ctx context.Context, data []byte) (map[string]any, error)
}

// SetDecoder makes the source read Confluent-framed records — the five-byte
// registry header plus an Avro body — instead of guessing at JSON.
//
// Once set, an unframed record is an error rather than something to fall back
// on. A topic that is not what the operator configured should say so, not
// quietly deliver bytes that will be wrong further downstream.
func (s *KafkaSource) SetDecoder(d recordDecoder) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.decoder = d
}

// buildMessage turns one record's bytes into a message.
//
// It is separate from Read so the decode path can be tested without a broker;
// Read supplies nothing but the bytes and the coordinates.
func (s *KafkaSource) buildMessage(
	ctx context.Context, value []byte, topic string, partition int, offset int64,
) (*message.DefaultMessage, error) {
	s.mu.Lock()
	dec := s.decoder
	s.mu.Unlock()

	msg := message.AcquireMessage()
	msg.SetPayload(value)

	switch {
	case dec != nil:
		// Configured for a registry topic: a record that does not decode is a
		// poison message, not something to deliver half-built.
		data, err := dec.Decode(ctx, value)
		if err != nil {
			msg.Release()
			return nil, fmt.Errorf("kafka source: topic %s partition %d offset %d: %w",
				topic, partition, offset, err)
		}
		for k, v := range data {
			msg.SetData(k, v)
		}

	default:
		// Try to unmarshal JSON into Data() for dynamic structure
		var jsonData map[string]any
		if err := json.Unmarshal(value, &jsonData); err == nil {
			for k, v := range jsonData {
				msg.SetData(k, v)
			}
		} else {
			msg.SetAfter(value) // Fallback for non-JSON
		}
	}

	msg.SetMetadata("kafka_topic", topic)
	msg.SetMetadata("kafka_partition", strconv.Itoa(partition))
	msg.SetMetadata("kafka_offset", strconv.FormatInt(offset, 10))
	return msg, nil
}

func (s *KafkaSource) Read(ctx context.Context) (hermod.Message, error) {
	s.mu.Lock()
	reader := s.reader
	s.mu.Unlock()

	m, err := reader.FetchMessage(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch message from kafka: %w", err)
	}

	msg, err := s.buildMessage(ctx, m.Value, m.Topic, m.Partition, m.Offset)
	if err != nil {
		return nil, err
	}
	msg.SetID(string(m.Key))

	return msg, nil
}

func (s *KafkaSource) Ack(ctx context.Context, msg hermod.Message) error {
	// A nil message must surface as an error rather than a dereference. The
	// engine acknowledges on the hot path, so a crash — or, as this actually
	// behaved, a goroutine that never returns — takes the consumer with it.
	if msg == nil {
		return errors.New("kafka source: nil message")
	}

	topic := msg.Metadata()["kafka_topic"]
	partitionStr := msg.Metadata()["kafka_partition"]
	offsetStr := msg.Metadata()["kafka_offset"]

	if topic == "" || partitionStr == "" || offsetStr == "" {
		return errors.New("missing kafka metadata in message")
	}

	var partition int
	var offset int64
	fmt.Sscanf(partitionStr, "%d", &partition)
	fmt.Sscanf(offsetStr, "%d", &offset)

	s.mu.Lock()
	reader := s.reader
	s.mu.Unlock()

	err := reader.CommitMessages(ctx, kafka.Message{
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

	s.mu.Lock()
	reader := s.reader
	brokers := s.brokers
	topic := s.topic
	s.mu.Unlock()

	// Check if brokers are reachable and topic exists
	conn, err := reader.Config().Dialer.DialContext(ctx, "tcp", brokers[0])
	if err != nil {
		return fmt.Errorf("failed to dial kafka broker %s: %w", brokers[0], err)
	}
	defer conn.Close()

	partitions, err := conn.ReadPartitions(topic)
	if err != nil {
		return fmt.Errorf("failed to read partitions for topic '%s': %w. Ensure topic exists and user has permissions", topic, err)
	}

	if len(partitions) == 0 {
		return fmt.Errorf("kafka topic '%s' has no partitions", topic)
	}

	return nil
}

func (s *KafkaSource) Ping(ctx context.Context) error {
	s.mu.Lock()
	reader := s.reader
	brokers := s.brokers
	s.mu.Unlock()

	// Try to dial first broker
	conn, err := reader.Config().Dialer.DialContext(ctx, "tcp", brokers[0])
	if err != nil {
		return fmt.Errorf("kafka ping failed for broker %s: %w", brokers[0], err)
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
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reader != nil {
		err := s.reader.Close()
		s.reader = nil
		return err
	}
	return nil
}
