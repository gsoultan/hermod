package dynamics365

import (
	"testing"

	"github.com/user/hermod/pkg/comm/message"
)

// The persisted cursor moves on Ack and on nothing else.
//
// This source advanced it inside the read path, before the record had been
// handed back, while Ack did nothing. GetState is what the engine writes down,
// so that cursor was already past records still in flight: a crash between
// reading and the sinks writing erased them from the resume, silently, because
// on restart the source began after them.
//
// It is the same defect fixed in eleven other sources here. It was left for
// later once, deliberately, while the vendor-pagination sources were being
// separated from the watermark ones — this is a watermark one: a plain ID
// field compared with OData's `gt`, not an opaque cursor whose meaning only
// the vendor knows.
//
// These need no server: the half that decides whether data survives a restart
// is the state contract, not the network.
func TestGetStateIsEmptyUntilSomethingIsAcknowledged(t *testing.T) {
	src := NewSource(SourceConfig{IDField: "id"}, nil)

	if got := src.GetState()["last_id"]; got != "" {
		t.Errorf("a source that has acknowledged nothing reports a cursor of %q; "+
			"a cursor that exists before an acknowledgement is one that can be "+
			"ahead of records still in flight", got)
	}
}

func TestAckMovesThePersistedCursor(t *testing.T) {
	src := NewSource(SourceConfig{IDField: "id"}, nil)

	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetID("r-1")
	msg.SetMetadata(watermarkKey, "42")

	if err := src.Ack(t.Context(), msg); err != nil {
		t.Fatalf("ack: %v", err)
	}
	if got := src.GetState()["last_id"]; got != "42" {
		t.Errorf("after acknowledging the record carrying watermark 42 the cursor "+
			"is %q, want 42", got)
	}
}

// A record with no watermark must leave the cursor alone rather than clearing
// it.
func TestAckWithoutAWatermarkLeavesTheCursorAlone(t *testing.T) {
	src := NewSource(SourceConfig{IDField: "id"}, nil)
	src.SetState(map[string]string{"last_id": "7"})

	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetID("r-2")

	if err := src.Ack(t.Context(), msg); err != nil {
		t.Fatalf("ack: %v", err)
	}
	if got := src.GetState()["last_id"]; got != "7" {
		t.Errorf("an acknowledgement carrying no watermark moved the cursor to %q, want 7", got)
	}
}

// A nil acknowledgement must not panic: the conformance suite feeds every
// source one, because a worker goroutine that dereferences it takes the engine
// down.
func TestAckOfNilDoesNotPanic(t *testing.T) {
	src := NewSource(SourceConfig{IDField: "id"}, nil)

	if err := src.Ack(t.Context(), nil); err != nil {
		t.Fatalf("ack(nil): %v", err)
	}
}
