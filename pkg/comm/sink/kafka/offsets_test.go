package kafka

import (
	"strings"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

func kafkaMsg(t *testing.T, group, member, gen, topic, part, off string) hermod.Message {
	t.Helper()
	m := message.AcquireMessage()
	t.Cleanup(m.Release)
	set := func(k, v string) {
		if v != "" {
			m.SetMetadata(k, v)
		}
	}
	set("kafka_group", group)
	set("kafka_member_id", member)
	set("kafka_generation", gen)
	set("kafka_topic", topic)
	set("kafka_partition", part)
	set("kafka_offset", off)
	return m
}

// The offsets a transaction commits are the next offset to read, per
// partition: one past the highest record in the batch. Committing the record's
// own offset would replay it after a restart; committing the lowest would
// replay the rest of the batch.
func TestSourceOffsetsCommitOnePastTheHighestRecordPerPartition(t *testing.T) {
	msgs := []hermod.Message{
		kafkaMsg(t, "g", "m1", "3", "orders", "0", "10"),
		kafkaMsg(t, "g", "m1", "3", "orders", "0", "12"),
		kafkaMsg(t, "g", "m1", "3", "orders", "0", "11"),
		kafkaMsg(t, "g", "m1", "3", "orders", "1", "4"),
		kafkaMsg(t, "g", "m1", "3", "payments", "0", "0"),
	}

	got, err := sourceOffsets(msgs)
	if err != nil {
		t.Fatalf("sourceOffsets: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want one group, got %d: %+v", len(got), got)
	}
	g := got[0]
	if g.group != "g" || g.member != "m1" || g.generation != 3 {
		t.Fatalf("group identity = %q/%q/%d, want g/m1/3", g.group, g.member, g.generation)
	}
	want := map[string]map[int32]int64{
		"orders":   {0: 13, 1: 5},
		"payments": {0: 1},
	}
	for topic, parts := range want {
		for p, off := range parts {
			if g.offsets[topic][p] != off {
				t.Errorf("%s/%d = %d, want %d", topic, p, g.offsets[topic][p], off)
			}
		}
	}
}

// A message that did not come from a Kafka consumer group has no offset to
// commit. It still belongs in the transaction, it just adds nothing to it.
func TestMessagesFromOtherSourcesAddNoOffsets(t *testing.T) {
	plain := message.AcquireMessage()
	t.Cleanup(plain.Release)
	noGroup := kafkaMsg(t, "", "", "", "orders", "0", "7")

	got, err := sourceOffsets([]hermod.Message{plain, noGroup})
	if err != nil {
		t.Fatalf("sourceOffsets: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want no offsets, got %+v", got)
	}
}

// Metadata that claims to be from a consumer group but cannot be read is an
// error: guessing would commit the wrong position inside a transaction that
// otherwise succeeds, and that is silent loss or replay.
func TestUnreadableSourceOffsetsFailTheBatch(t *testing.T) {
	cases := map[string]hermod.Message{
		"partition":  kafkaMsg(t, "g", "m", "1", "orders", "x", "1"),
		"offset":     kafkaMsg(t, "g", "m", "1", "orders", "0", ""),
		"generation": kafkaMsg(t, "g", "m", "one", "orders", "0", "1"),
		"topic":      kafkaMsg(t, "g", "m", "1", "", "0", "1"),
	}
	for name, m := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := sourceOffsets([]hermod.Message{m}); err == nil {
				t.Fatal("want an error")
			}
		})
	}
}

// Two generations of the same group in one batch means a rebalance happened
// between records. Only one generation can be committed; the older one would
// be fenced, so the batch is refused rather than half-committed.
func TestTwoGenerationsOfOneGroupInABatchAreRefused(t *testing.T) {
	msgs := []hermod.Message{
		kafkaMsg(t, "g", "m1", "3", "orders", "0", "1"),
		kafkaMsg(t, "g", "m2", "4", "orders", "1", "1"),
	}
	_, err := sourceOffsets(msgs)
	if err == nil || !strings.Contains(err.Error(), "generation") {
		t.Fatalf("want a generation error, got %v", err)
	}
}

// Writing must not change the messages it was handed: the engine may still
// hand the same message to another sink.
func TestSourceOffsetsDoesNotMutateMessages(t *testing.T) {
	m := kafkaMsg(t, "g", "m1", "3", "orders", "0", "10")
	before := len(m.Metadata())
	if _, err := sourceOffsets([]hermod.Message{m}); err != nil {
		t.Fatal(err)
	}
	if len(m.Metadata()) != before {
		t.Fatal("metadata changed")
	}
}

func TestTransactionalSinkReportsItsTransactionalID(t *testing.T) {
	s := NewTransactionalKafkaSink([]string{"127.0.0.1:1"}, "t", "", "", "hermod-tx", nil)
	t.Cleanup(func() { _ = s.Close() })
	if got := s.TransactionalID(); got != "hermod-tx" {
		t.Fatalf("TransactionalID() = %q", got)
	}
	plain := NewKafkaSink([]string{"127.0.0.1:1"}, "t", "", "", nil)
	t.Cleanup(func() { _ = plain.Close() })
	if plain.TransactionalID() != "" {
		t.Fatal("a plain sink reports a transactional id")
	}
}

// A message without an ID must produce a record without a key. An empty but
// non-nil key is still hashed, so every keyless record would land on one
// partition instead of being spread.
func TestAMessageWithoutAnIDIsWrittenWithoutAKey(t *testing.T) {
	s := NewKafkaSink([]string{"127.0.0.1:1"}, "t", "", "", nil)
	t.Cleanup(func() { _ = s.Close() })

	keyed := message.AcquireMessage()
	t.Cleanup(keyed.Release)
	keyed.SetID("k1")
	keyless := message.AcquireMessage()
	t.Cleanup(keyless.Release)

	recs, err := s.records([]hermod.Message{keyed, keyless})
	if err != nil {
		t.Fatal(err)
	}
	if string(recs[0].Key) != "k1" {
		t.Errorf("keyed record key = %q", recs[0].Key)
	}
	if recs[1].Key != nil {
		t.Errorf("keyless record has key %q (non-nil), want nil", recs[1].Key)
	}
}
