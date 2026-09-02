package kafka

import (
	"context"
	"fmt"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/sasl/plain"
)

type KafkaSink struct {
	writer    *kafka.Writer
	transport *kafka.Transport
	formatter hermod.Formatter
}

// NewKafkaSink builds an at-least-once Kafka sink.
//
// It previously took a transactionalID, which was stored and never read — the
// writer was never configured for transactions. The parameter was removed
// rather than left in place, because a knob that silently does nothing is how
// an operator ends up believing they have exactly-once delivery. See the note
// above Write for what real EOS support would require.
func NewKafkaSink(brokers []string, topic string, username, password string, formatter hermod.Formatter) *KafkaSink {
	// Always own a Transport, even without SASL.
	//
	// Leaving this nil makes kafka-go fall back to its package-level
	// DefaultTransport, which is shared process-wide and keeps background
	// connection and metadata-refresh goroutines alive against every broker any
	// sink has ever touched. Those goroutines outlive Close, so a torn-down
	// pipeline keeps dialling a broker it no longer writes to, and a workflow
	// that is edited repeatedly accumulates them for the life of the process.
	//
	// Owning the Transport means CloseIdleConnections in Close actually reclaims
	// them.
	transport := &kafka.Transport{}
	if username != "" {
		transport.SASL = plain.Mechanism{
			Username: username,
			Password: password,
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

// NOTE: KafkaSink deliberately does NOT implement hermod.Transactional or
// hermod.TwoPhaseCommit.
//
// It previously did, with six methods that all returned nil. Because the 2PC
// contract reports failure through the error return, a no-op Rollback tells a
// coordinator the rollback succeeded while the records remain committed on the
// broker — the transaction silently diverges instead of failing loudly.
//
// Honest at-least-once delivery is strictly better than a false atomicity
// guarantee, so the methods are gone and callers' type assertions now correctly
// evaluate to false.
//
// Implementing this properly requires a transactional producer
// (InitTransactions / BeginTransaction / AbortTransaction). segmentio/kafka-go
// does not expose one, so real Kafka EOS is blocked on migrating to franz-go or
// confluent-kafka-go. pkg/comm/sink/kafka/twopc_test.go fails the build if the
// interface is satisfied again without that work.

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
	// Never let the client's own timeout outlive the caller's deadline. Ping is
	// the readiness path, called behind a probe timeout of a few seconds; a
	// hardcoded 10s here means the probe gives up while this call is still
	// running, leaving a goroutine and a connection behind on every scrape.
	timeout := 10 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining < timeout {
			timeout = remaining
		}
	}

	client := &kafka.Client{
		Addr:      s.writer.Addr,
		Transport: s.transport,
		Timeout:   timeout,
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
	err := s.writer.Close()
	// Release the transport's pooled connections and their background
	// goroutines. Without this they survive the sink that created them.
	if s.transport != nil {
		s.transport.CloseIdleConnections()
	}
	return err
}
