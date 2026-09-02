package control

import (
	"context"
	"errors"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/engine/registry/interfaces"
	"github.com/gsoultan/hermod/internal/storage"
	msgpkg "github.com/gsoultan/hermod/pkg/comm/message"
)

// ---------------------------------------------------------------------------
// Nodes that park a message must not lose it when the parking fails.
//
// Several control nodes hand a message to storage and return none, because the
// message is meant to leave the pipeline and come back later — when a timer
// fires, when a human approves, when the rest of a batch arrives. That is only
// sound while the write succeeds, and each of them discarded its error.
//
// The Wait node showed what that costs: its table did not exist, so the write
// always failed, and every message passing through a long wait was destroyed
// while the log said "Message suspended". These are the same shape.
// ---------------------------------------------------------------------------

// rejectingStorage fails every write a control node makes.
type rejectingStorage struct {
	interfaces.RegistryStorage
	err error
}

func (r *rejectingStorage) CreateApproval(context.Context, storage.Approval) error { return r.err }

// rejectingStateStore fails every Set, and records Deletes. getData is what a
// Get returns, so a partly-collected group can be set up.
type rejectingStateStore struct {
	setErr    error
	deleteErr error
	deletes   int
	getData   []byte
}

func (r *rejectingStateStore) Get(context.Context, string) ([]byte, error) {
	return r.getData, nil
}
func (r *rejectingStateStore) Set(context.Context, string, []byte) error { return r.setErr }
func (r *rejectingStateStore) Delete(context.Context, string) error {
	r.deletes++
	return r.deleteErr
}

// storeCtx supplies a storage and a state store to a node under test.
type storeCtx struct {
	stubCtx
	store interfaces.RegistryStorage
	state hermod.StateStore
	logs  []string
}

func (s *storeCtx) Storage() interfaces.RegistryStorage { return s.store }
func (s *storeCtx) StateStore() hermod.StateStore       { return s.state }
func (s *storeCtx) BroadcastLog(_, level, msg, _ string) {
	s.logs = append(s.logs, level+": "+msg)
}

func (s *storeCtx) loggedError() bool {
	for _, l := range s.logs {
		if len(l) >= 5 && l[:5] == "ERROR" {
			return true
		}
	}
	return false
}

func controlMessage(t *testing.T) hermod.Message {
	t.Helper()
	m := msgpkg.AcquireMessage()
	m.SetID("m-1")
	t.Cleanup(func() { msgpkg.ReleaseMessage(m) })
	return m
}

// fanoutMessage is a message inside a fan-out group, which is the only shape
// the collect node acts on — it reads the group and total from metadata.
func fanoutMessage(t *testing.T, group, total string) hermod.Message {
	t.Helper()
	m := controlMessage(t)
	m.SetMetadata("_fanout_group", group)
	m.SetMetadata("_fanout_total", total)
	return m
}

// TestApprovalDoesNotLoseAMessageItCannotRecord. The node returns no messages
// because the workflow resumes from the approval record. With no record there
// is nothing for anyone to approve, and the message is simply gone.
func TestApprovalDoesNotLoseAMessageItCannotRecord(t *testing.T) {
	nctx := &storeCtx{store: &rejectingStorage{err: errors.New("database is down")}}
	node := &storage.WorkflowNode{ID: "ap-1", Type: "approval"}

	out, _, err := (&ApprovalNode{}).Execute(t.Context(), nctx, "wf-1", node, controlMessage(t))

	if err == nil {
		t.Fatal("the node reported a pending approval it never recorded; nobody can " +
			"approve it and the message has left the pipeline, so it is lost")
	}
	if len(out) != 0 {
		t.Errorf("emitted %d message(s); a failed approval should surface, not continue", len(out))
	}
}

// TestApprovalStillHaltsWhenRecorded is the other half: the ordinary path must
// still park the message.
func TestApprovalStillHaltsWhenRecorded(t *testing.T) {
	nctx := &storeCtx{store: &rejectingStorage{err: nil}}
	node := &storage.WorkflowNode{ID: "ap-2", Type: "approval"}

	out, status, err := (&ApprovalNode{}).Execute(t.Context(), nctx, "wf-1", node, controlMessage(t))
	if err != nil {
		t.Fatalf("a recorded approval returned an error: %v", err)
	}
	if status != "pending" {
		t.Errorf("status = %q, want pending", status)
	}
	if len(out) != 0 {
		t.Errorf("emitted %d message(s); the workflow resumes from the approval record", len(out))
	}
}

// TestCollectDoesNotLoseAnItemItCannotStore is worse than one message. The
// group only emits once it reaches its total, so an item that is not stored
// means the total is never reached and every message in that group is lost.
func TestCollectDoesNotLoseAnItemItCannotStore(t *testing.T) {
	nctx := &storeCtx{state: &rejectingStateStore{setErr: errors.New("state store unavailable")}}
	node := &storage.WorkflowNode{ID: "co-1", Type: "collect"}

	out, _, err := (&CollectNode{}).Execute(
		t.Context(), nctx, "wf-1", node, fanoutMessage(t, "g1", "3"))

	if err == nil {
		t.Fatal("the node accepted an item it could not store; the group will never " +
			"reach its total, so every message collected into it is lost")
	}
	if len(out) != 0 {
		t.Errorf("emitted %d message(s) for an item that was not stored", len(out))
	}
}

// TestCollectReportsAFailedCleanup. A batch that emitted but could not clear its
// key leaves the items behind, so the next group with that id starts part-full
// and emits a batch containing another group's data. That must not be silent.
func TestCollectReportsAFailedCleanup(t *testing.T) {
	// One item is already collected, and this message is the second of two — so
	// the group completes and the node clears its key.
	state := &rejectingStateStore{
		deleteErr: errors.New("state store unavailable"),
		getData:   []byte(`[{"first":true}]`),
	}
	nctx := &storeCtx{state: state}
	node := &storage.WorkflowNode{ID: "co-2", Type: "collect"}

	out, _, err := (&CollectNode{}).Execute(
		t.Context(), nctx, "wf-1", node, fanoutMessage(t, "g2", "2"))

	// The batch completed, so it must still be emitted — failing here would
	// throw away work that succeeded.
	if err != nil {
		t.Fatalf("a completed batch was failed because its cleanup failed: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("a completed batch emitted %d message(s), want 1", len(out))
	}
	if state.deletes == 0 {
		t.Fatal("the collected key was never cleared")
	}
	if !nctx.loggedError() {
		t.Error("the failed cleanup was not logged; the next group with this id will " +
			"start with these items still in it and nothing will say why")
	}
}

// TestStatefulReportsAFailedSave. The message is not lost here — it carries the
// new value onward — but the value was not persisted, so the next message reads
// a stale one and the accumulator is quietly wrong from then on.
func TestStatefulReportsAFailedSave(t *testing.T) {
	nctx := &storeCtx{state: &rejectingStateStore{setErr: errors.New("state store unavailable")}}
	node := &storage.WorkflowNode{
		ID:     "st-1",
		Type:   "stateful",
		Config: map[string]any{"operation": "increment", "outputField": "n"},
	}

	out, _, err := (&StatefulNode{}).Execute(t.Context(), nctx, "wf-1", node, controlMessage(t))

	if err != nil {
		t.Fatalf("a stateful node failed the message over a save it could retry: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("emitted %d message(s), want 1", len(out))
	}
	if !nctx.loggedError() {
		t.Error("a counter that did not persist was not logged; every later message " +
			"reads a stale value and the totals are silently wrong")
	}
}
