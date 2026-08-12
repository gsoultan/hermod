package control

import (
	"context"
	"errors"
	"testing"

	"github.com/user/hermod/internal/engine/registry/interfaces"
	"github.com/user/hermod/internal/storage"
	msgpkg "github.com/user/hermod/pkg/comm/message"
)

// ---------------------------------------------------------------------------
// A wait that cannot be recorded must not swallow the message.
//
// A wait longer than thirty seconds is not held in memory. The node writes a
// suspended message to storage and returns nothing, so the message leaves the
// pipeline — the reconciler is expected to bring it back when the timer is up.
// That is sound only while the write succeeds.
//
// The write's error was discarded. On every SQL backend it always failed,
// because suspended_messages was defined in the query set but never created by
// Init, so a Wait node destroyed every message that passed through it and
// logged "Message suspended" while doing so.
//
// The missing table is fixed. This covers the other half: if the write fails
// for any other reason — the database is down, the disk is full, a constraint
// rejects it — the message must not vanish.
// ---------------------------------------------------------------------------

// failingSuspendStorage rejects every attempt to record a suspended message.
type failingSuspendStorage struct {
	interfaces.RegistryStorage
	err error
}

func (f *failingSuspendStorage) CreateSuspendedMessage(context.Context, storage.SuspendedMessage) error {
	return f.err
}

type suspendCtx struct {
	stubCtx
	store interfaces.RegistryStorage
}

func (s *suspendCtx) Storage() interfaces.RegistryStorage { return s.store }

func TestWaitDoesNotDropAMessageItCannotSuspend(t *testing.T) {
	n := &WaitNode{}
	node := &storage.WorkflowNode{
		ID:     "wait-1",
		Type:   "wait",
		Config: map[string]any{"duration": "10m"},
	}
	msg := msgpkg.AcquireMessage()
	defer msgpkg.ReleaseMessage(msg)
	msg.SetID("m-1")

	nctx := &suspendCtx{store: &failingSuspendStorage{err: errors.New("no such table: suspended_messages")}}

	out, _, err := n.Execute(t.Context(), nctx, "wf-1", node, msg)

	if err == nil {
		t.Fatal("the node reported success after failing to record the suspension; " +
			"it returns no messages, so the message is gone — silently, with a log " +
			"line saying it was suspended")
	}
	if len(out) != 0 {
		t.Errorf("a failed suspension emitted %d message(s); it should surface the error, "+
			"not guess at continuing", len(out))
	}
}

// TestWaitSuspendsWhenStorageAccepts is the other half: the ordinary path must
// still hand the message off and emit nothing.
func TestWaitSuspendsWhenStorageAccepts(t *testing.T) {
	n := &WaitNode{}
	node := &storage.WorkflowNode{
		ID:     "wait-2",
		Type:   "wait",
		Config: map[string]any{"duration": "10m"},
	}
	msg := msgpkg.AcquireMessage()
	defer msgpkg.ReleaseMessage(msg)
	msg.SetID("m-2")

	nctx := &suspendCtx{store: &failingSuspendStorage{err: nil}}

	out, status, err := n.Execute(t.Context(), nctx, "wf-2", node, msg)
	if err != nil {
		t.Fatalf("a suspension that stored cleanly returned an error: %v", err)
	}
	if status != "suspended" {
		t.Errorf("status = %q, want suspended", status)
	}
	if len(out) != 0 {
		t.Errorf("a suspended message emitted %d message(s); it resumes from storage, not here", len(out))
	}
}
