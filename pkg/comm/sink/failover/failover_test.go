package failover

import (
	"context"
	"errors"
	"testing"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/comm/message"
)

type mockSink struct {
	writeCalled bool
	fail        bool
	closeCalled bool
}

func (m *mockSink) Write(ctx context.Context, msg hermod.Message) error {
	m.writeCalled = true
	if m.fail {
		return errors.New("write failed")
	}
	return nil
}

func (m *mockSink) Ping(ctx context.Context) error { return nil }
func (m *mockSink) Close() error {
	m.closeCalled = true
	return nil
}

// batchSink is a mockSink that also takes batches, recording what it was
// handed and failing after a configurable number of rows to model a batch
// that dies partway through.
type batchSink struct {
	mockSink
	got       []string
	failAfter int // -1: accept everything; n>=0: fail once n rows are in
	batchErr  error
}

func (b *batchSink) WriteBatch(ctx context.Context, msgs []hermod.Message) error {
	for _, m := range msgs {
		if b.failAfter >= 0 && len(b.got) >= b.failAfter {
			if b.batchErr == nil {
				b.batchErr = errors.New("disk full on primary")
			}
			return b.batchErr
		}
		b.got = append(b.got, m.ID())
	}
	return nil
}

func batchOf(t *testing.T, ids ...string) []hermod.Message {
	t.Helper()
	msgs := make([]hermod.Message, 0, len(ids))
	for _, id := range ids {
		m := message.AcquireMessage()
		t.Cleanup(m.Release)
		m.SetID(id)
		msgs = append(msgs, m)
	}
	return msgs
}

// The batch path had no test at all: a failed primary batch must land, whole,
// on the fallback.
func TestABatchFailsOverWhole(t *testing.T) {
	primary := &batchSink{failAfter: 0}
	fallback := &batchSink{failAfter: -1}
	s := NewFailoverSink(primary, []hermod.Sink{fallback})

	if err := s.WriteBatch(t.Context(), batchOf(t, "a", "b", "c")); err != nil {
		t.Fatalf("the fallback accepted the batch and the group still failed: %v", err)
	}
	if len(fallback.got) != 3 {
		t.Errorf("the fallback received %d of 3 messages", len(fallback.got))
	}
}

// When every sink refuses the batch, the error must say why — it carried no
// underlying cause at all, so an operator saw "all sinks failed" with three
// different reasons hidden behind it.
func TestAnExhaustedGroupNamesTheCauses(t *testing.T) {
	primary := &batchSink{failAfter: 0, batchErr: errors.New("primary: connection refused")}
	fallback := &batchSink{failAfter: 0, batchErr: errors.New("fallback: table missing")}
	s := NewFailoverSink(primary, []hermod.Sink{fallback})

	err := s.WriteBatch(t.Context(), batchOf(t, "a"))
	if err == nil {
		t.Fatal("every sink failed and the group reported success")
	}
	if !errors.Is(err, fallback.batchErr) {
		t.Errorf("the group error does not carry the last cause: %v", err)
	}
}

// The divergence hazard, pinned so it stays documented behaviour rather than
// becoming a surprise: a primary that dies partway through a batch has
// already committed the rows before the failure, and the fallback then gets
// the whole batch — so the leading rows exist in both destinations. That is
// the price of failover under at-least-once, it cannot be deduplicated
// across two different stores, and anyone routing a failover group into
// side-by-side destinations needs to know it.
func TestAPartialPrimaryBatchDivergesAndTheFallbackGetsEverything(t *testing.T) {
	primary := &batchSink{failAfter: 2}
	fallback := &batchSink{failAfter: -1}
	s := NewFailoverSink(primary, []hermod.Sink{fallback})

	if err := s.WriteBatch(t.Context(), batchOf(t, "a", "b", "c")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if len(primary.got) != 2 || len(fallback.got) != 3 {
		t.Errorf("primary holds %d rows and the fallback %d; the documented "+
			"divergence is primary=2, fallback=3", len(primary.got), len(fallback.got))
	}
}

func TestFailoverSink_Write(t *testing.T) {
	primary := &mockSink{fail: true}
	fallback := &mockSink{fail: false}

	s := NewFailoverSink(primary, []hermod.Sink{fallback})
	msg := message.AcquireMessage()

	err := s.Write(t.Context(), msg)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !primary.writeCalled {
		t.Error("primary write should have been called")
	}
	if !fallback.writeCalled {
		t.Error("fallback write should have been called")
	}
}

func TestFailoverSink_Write_PrimarySuccess(t *testing.T) {
	primary := &mockSink{fail: false}
	fallback := &mockSink{fail: false}

	s := NewFailoverSink(primary, []hermod.Sink{fallback})
	msg := message.AcquireMessage()

	err := s.Write(t.Context(), msg)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !primary.writeCalled {
		t.Error("primary write should have been called")
	}
	if fallback.writeCalled {
		t.Error("fallback write should NOT have been called")
	}
}

func TestFailoverSink_Write_AllFail(t *testing.T) {
	primary := &mockSink{fail: true}
	fallback := &mockSink{fail: true}

	s := NewFailoverSink(primary, []hermod.Sink{fallback})
	msg := message.AcquireMessage()

	err := s.Write(t.Context(), msg)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestFailoverSink_Close(t *testing.T) {
	primary := &mockSink{}
	fallback := &mockSink{}

	s := NewFailoverSink(primary, []hermod.Sink{fallback})
	s.Close()

	if !primary.closeCalled {
		t.Error("primary close should have been called")
	}
	if !fallback.closeCalled {
		t.Error("fallback close should have been called")
	}
}
