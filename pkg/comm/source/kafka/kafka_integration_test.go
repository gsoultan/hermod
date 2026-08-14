//go:build integration

package kafka

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"
	"github.com/user/hermod/pkg/comm/message"
	sinkkafka "github.com/user/hermod/pkg/comm/sink/kafka"
)

// The Kafka data path, against a real broker.
//
// Kafka is Beta, which in this repository means substantial and unit-tested but
// never proven to move a record. It is also one of the connectors people reach
// for first, so "probably fine" is a weaker position than it sounds — every
// other connector taken from that state to a live test this week turned out to
// have something wrong with it.
//
// Two properties are worth the broker. That a record written by the sink comes
// back out of the source intact, and that acknowledging it moves the consumer
// group's offset — because an offset that does not move means a restart
// redelivers everything, and an offset that moves too early means a restart
// skips what was never delivered.
//
// Run with:
//
//	HERMOD_INTEGRATION=1 KAFKA_BROKERS=127.0.0.1:9092 \
//	go test -tags=integration ./pkg/comm/source/kafka/

func requireKafka(t *testing.T) ([]string, string) {
	t.Helper()
	raw := os.Getenv("KAFKA_BROKERS")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || raw == "" {
		if os.Getenv("GITHUB_ACTIONS") == "true" {
			t.Fatalf("HERMOD_INTEGRATION=%q KAFKA_BROKERS=%q in CI, where a broker is started "+
				"for exactly this", os.Getenv("HERMOD_INTEGRATION"), raw)
		}
		t.Skip("integration: set HERMOD_INTEGRATION=1 and KAFKA_BROKERS to run")
	}
	brokers := strings.Split(raw, ",")
	topic := fmt.Sprintf("hermod_it_%d", time.Now().UnixNano())

	// Created explicitly. The sink does not create topics and does not ask the
	// broker to auto-create them, so a missing topic is a write error rather
	// than something that quietly appears — which is the right default for a
	// sink, and worth the test setting up honestly.
	conn, err := kafkago.Dial("tcp", brokers[0])
	if err != nil {
		t.Fatalf("dialling %s: %v", brokers[0], err)
	}
	defer conn.Close()
	if err := conn.CreateTopics(kafkago.TopicConfig{
		Topic: topic, NumPartitions: 1, ReplicationFactor: 1,
	}); err != nil {
		t.Fatalf("creating topic %s: %v", topic, err)
	}
	t.Cleanup(func() {
		c, err := kafkago.Dial("tcp", brokers[0])
		if err == nil {
			_ = c.DeleteTopics(topic)
			_ = c.Close()
		}
	})
	return brokers, topic
}

// produce writes n messages through the sink, which is the path a workflow uses.
func produce(t *testing.T, brokers []string, topic string, n int) {
	t.Helper()
	snk := sinkkafka.NewKafkaSink(brokers, topic, "", "", nil)
	t.Cleanup(func() { _ = snk.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	for i := range n {
		msg := message.AcquireMessage()
		msg.SetID(fmt.Sprintf("key-%d", i))
		body, _ := json.Marshal(map[string]any{"seq": i, "name": "ada"})
		msg.SetPayload(body)
		if err := snk.Write(ctx, msg); err != nil {
			t.Fatalf("producing %d: %v", i, err)
		}
		msg.Release()
	}
}

// A record written by the sink must come back out of the source intact.
func TestARecordWrittenBySinkComesBackOutOfTheSource(t *testing.T) {
	brokers, topic := requireKafka(t)
	const total = 5
	produce(t, brokers, topic, total)

	src := NewKafkaSource(brokers, topic, "grp_"+topic, "", "")
	t.Cleanup(func() { _ = src.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()

	seen := map[int]string{}
	for len(seen) < total {
		msg, err := src.Read(ctx)
		if err != nil {
			t.Fatalf("read after %d of %d: %v", len(seen), total, err)
		}
		if msg == nil {
			continue
		}
		seq, ok := msg.Data()["seq"].(float64)
		if !ok {
			t.Fatalf("message carries no seq field; data=%v payload=%q", msg.Data(), msg.Payload())
		}
		seen[int(seq)] = msg.ID()
		if err := src.Ack(ctx, msg); err != nil {
			t.Fatalf("ack: %v", err)
		}
	}

	for i := range total {
		if id, ok := seen[i]; !ok {
			t.Errorf("record seq=%d never arrived", i)
		} else if id != fmt.Sprintf("key-%d", i) {
			t.Errorf("record seq=%d came back with id %q, want %q — the sink writes the "+
				"message id as the Kafka key and the source reads it back, so losing it "+
				"costs sink-side idempotency its dedup key", i, id, fmt.Sprintf("key-%d", i))
		}
	}
}

// Acknowledging must move the consumer group's offset, or a restart replays
// everything that was already delivered.
func TestAcknowledgedRecordsAreNotRedeliveredToTheSameGroup(t *testing.T) {
	brokers, topic := requireKafka(t)
	group := "grp_" + topic
	const total = 4
	produce(t, brokers, topic, total)

	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()

	first := NewKafkaSource(brokers, topic, group, "", "")
	for i := range total {
		msg, err := first.Read(ctx)
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		if err := first.Ack(ctx, msg); err != nil {
			t.Fatalf("ack %d: %v", i, err)
		}
	}
	if err := first.Close(); err != nil {
		t.Fatalf("closing the first consumer: %v", err)
	}

	// One new record, so there is something to prove the replacement consumer
	// is live rather than merely silent.
	produce(t, brokers, topic, 1)

	second := NewKafkaSource(brokers, topic, group, "", "")
	t.Cleanup(func() { _ = second.Close() })

	readCtx, readCancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer readCancel()

	msg, err := second.Read(readCtx)
	if err != nil {
		t.Fatalf("the replacement consumer read nothing: %v", err)
	}
	seq, _ := msg.Data()["seq"].(float64)
	if int(seq) != 0 {
		// produce() restarts numbering, so the new record is seq=0. Anything
		// from the first batch means the offset never moved.
		t.Errorf("after acknowledging %d records the replacement consumer was handed seq=%d\n"+
			"the committed offset did not advance, so every restart replays what was "+
			"already delivered", total, int(seq))
	}
}
