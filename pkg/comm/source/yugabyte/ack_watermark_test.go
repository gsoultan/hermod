package yugabyte

import (
	"testing"
	"time"

	"github.com/user/hermod/pkg/comm/message"
)

// The persisted cursor moves on Ack and on nothing else.
//
// This source advanced it inside the read loop, before the row had even been
// handed back, while Ack did nothing. GetState is what the engine writes down,
// so that cursor was already past rows still in flight: a crash between
// reading and the sinks writing erased them from the resume, silently, because
// on restart the source began after them. Ten sources in this repository
// carried that defect.
//
// These assertions need no server: they exercise the state contract itself,
// which is the half that decides whether data survives a restart.
func TestGetStateIsEmptyUntilSomethingIsAcknowledged(t *testing.T) {
	src := NewYugabyteSource("dsn", []string{"T"}, "ID", time.Second, true)
	t.Cleanup(func() { _ = src.Close() })

	if got := src.GetState(); len(got) != 0 {
		t.Errorf("a source that has acknowledged nothing reports state %v; a "+
			"cursor that exists before an acknowledgement is one that can be "+
			"ahead of rows still in flight", got)
	}
}

func TestAckMovesThePersistedCursor(t *testing.T) {
	src := NewYugabyteSource("dsn", []string{"T"}, "ID", time.Second, true)
	t.Cleanup(func() { _ = src.Close() })

	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetID("r-1")
	msg.SetMetadata(watermarkKey, "42")
	msg.SetMetadata(watermarkTableKey, "T")

	if err := src.Ack(t.Context(), msg); err != nil {
		t.Fatalf("ack: %v", err)
	}
	if got := src.GetState()["last_id:T"]; got != "42" {
		t.Errorf("after acknowledging the row carrying watermark 42 the cursor "+
			"is %q, want 42", got)
	}
}

// A row with no watermark must leave the cursor alone rather than clearing it.
func TestAckWithoutAWatermarkLeavesTheCursorAlone(t *testing.T) {
	src := NewYugabyteSource("dsn", []string{"T"}, "ID", time.Second, true)
	t.Cleanup(func() { _ = src.Close() })

	carried := map[string]string{"last_id:T": "7"}
	src.SetState(carried)

	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetID("r-2")

	if err := src.Ack(t.Context(), msg); err != nil {
		t.Fatalf("ack: %v", err)
	}
	if got := src.GetState()["last_id:T"]; got != "7" {
		t.Errorf("an acknowledgement carrying no watermark moved the cursor to %q, want 7", got)
	}
}

// A nil acknowledgement must not panic: the conformance suite feeds every
// source one, because a worker goroutine that dereferences it takes the whole
// engine down.
func TestAckOfNilDoesNotPanic(t *testing.T) {
	src := NewYugabyteSource("dsn", []string{"T"}, "ID", time.Second, true)
	t.Cleanup(func() { _ = src.Close() })

	if err := src.Ack(t.Context(), nil); err != nil {
		t.Fatalf("ack(nil): %v", err)
	}
}

var _ = time.Second
