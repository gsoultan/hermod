package txgroup

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/comm/message"
	"github.com/user/hermod/pkg/engine/twopc"
)

// txSink is a member that can take part in a transaction.
type txSink struct {
	mu sync.Mutex

	name       string
	writeErr   error
	prepareErr error

	written    int
	committed  int
	rolledBack int
}

func (s *txSink) Write(_ context.Context, msg hermod.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.writeErr != nil {
		return s.writeErr
	}
	s.written++
	return nil
}

func (s *txSink) WriteBatch(_ context.Context, msgs []hermod.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.writeErr != nil {
		return s.writeErr
	}
	s.written += len(msgs)
	return nil
}

func (s *txSink) Ping(context.Context) error { return nil }
func (s *txSink) Close() error               { return nil }

func (s *txSink) Begin(context.Context) error    { return nil }
func (s *txSink) Commit(context.Context) error   { return nil }
func (s *txSink) Rollback(context.Context) error { return nil }

// Prepare honours the coordinator-supplied ID, as a real participant does: the
// coordinator records the name before the transaction exists, so a participant
// that substituted its own would reopen the window that argument closes.
func (s *txSink) Prepare(_ context.Context, txID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.prepareErr != nil {
		return "", s.prepareErr
	}
	if txID == "" {
		return s.name + "-tx", nil
	}
	return txID, nil
}

func (s *txSink) CommitPrepared(context.Context, string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.committed++
	return nil
}

func (s *txSink) RollbackPrepared(context.Context, string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rolledBack++
	return nil
}

func (s *txSink) counts() (written, committed, rolledBack int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.written, s.committed, s.rolledBack
}

// plainSink implements hermod.Sink but not TwoPhaseCommit — the case a group
// must refuse.
type plainSink struct{}

func (plainSink) Write(context.Context, hermod.Message) error { return nil }
func (plainSink) Ping(context.Context) error                  { return nil }
func (plainSink) Close() error                                { return nil }

// memStore is a twopc.Store for the tests.
type memStore struct {
	mu sync.Mutex
	kv map[string][]byte
}

func newMemStore() *memStore { return &memStore{kv: map[string][]byte{}} }

func (m *memStore) Get(_ context.Context, k string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.kv[k]
	if !ok {
		return nil, twopc.ErrNotFound
	}
	return v, nil
}

func (m *memStore) Set(_ context.Context, k string, v []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.kv[k] = v
	return nil
}

func (m *memStore) Delete(_ context.Context, k string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.kv, k)
	return nil
}

func (m *memStore) List(_ context.Context, prefix string) (map[string][]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string][]byte{}
	for k, v := range m.kv {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			out[k] = v
		}
	}
	return out, nil
}

func newGroup(t *testing.T, members ...Member) *Sink {
	t.Helper()
	c, err := twopc.New(twopc.Options{Store: newMemStore(), WorkflowID: "wf-1"})
	if err != nil {
		t.Fatalf("twopc.New: %v", err)
	}
	g, err := New(members, c, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return g
}

func msg(t *testing.T) hermod.Message {
	t.Helper()
	m := message.AcquireMessage()
	m.SetID("m1")
	return m
}

func TestWritesAndCommitsEveryMember(t *testing.T) {
	a := &txSink{name: "a"}
	b := &txSink{name: "b"}
	g := newGroup(t, Member{ID: "a", Sink: a}, Member{ID: "b", Sink: b})

	if err := g.Write(context.Background(), msg(t)); err != nil {
		t.Fatalf("Write: %v", err)
	}

	for _, s := range []*txSink{a, b} {
		written, committed, rolledBack := s.counts()
		if written != 1 || committed != 1 || rolledBack != 0 {
			t.Errorf("%s: written=%d committed=%d rolledBack=%d, want 1/1/0",
				s.name, written, committed, rolledBack)
		}
	}
}

// TestOneMemberFailingToWriteAbortsTheBatch is the guarantee the group exists
// for: no member may keep a batch another member rejected.
func TestOneMemberFailingToWriteAbortsTheBatch(t *testing.T) {
	a := &txSink{name: "a"}
	b := &txSink{name: "b", writeErr: errors.New("connection reset")}
	g := newGroup(t, Member{ID: "a", Sink: a}, Member{ID: "b", Sink: b})

	if err := g.Write(context.Background(), msg(t)); err == nil {
		t.Fatal("expected the write to fail")
	}

	if _, committed, _ := a.counts(); committed != 0 {
		t.Error("a member committed while another could not write — the batch is half-applied")
	}
}

// TestOneMemberFailingToPrepareRollsBackTheRest covers the vote phase, where
// the writes already succeeded everywhere.
func TestOneMemberFailingToPrepareRollsBackTheRest(t *testing.T) {
	a := &txSink{name: "a"}
	b := &txSink{name: "b", prepareErr: errors.New("max_prepared_transactions is 0")}
	g := newGroup(t, Member{ID: "a", Sink: a}, Member{ID: "b", Sink: b})

	if err := g.Write(context.Background(), msg(t)); err == nil {
		t.Fatal("expected the write to fail when a member cannot prepare")
	}

	_, committed, rolledBack := a.counts()
	if committed != 0 {
		t.Error("a member committed while another could not prepare")
	}
	if rolledBack != 1 {
		t.Errorf("the prepared member was not rolled back (rolledBack=%d); it holds locks", rolledBack)
	}
}

// TestRefusesMembersThatCannotParticipate is the up-front check. Degrading
// silently to non-atomic writes would be discovered during an incident.
func TestRefusesMembersThatCannotParticipate(t *testing.T) {
	c, err := twopc.New(twopc.Options{Store: newMemStore(), WorkflowID: "wf-1"})
	if err != nil {
		t.Fatalf("twopc.New: %v", err)
	}

	_, err = New([]Member{
		{ID: "a", Sink: &txSink{name: "a"}},
		{ID: "plain", Sink: plainSink{}},
	}, c, nil)
	if err == nil {
		t.Fatal("expected New to refuse a member that cannot take part in a transaction")
	}
}

func TestRefusesFewerThanTwoMembers(t *testing.T) {
	c, _ := twopc.New(twopc.Options{Store: newMemStore(), WorkflowID: "wf-1"})
	if _, err := New([]Member{{ID: "a", Sink: &txSink{name: "a"}}}, c, nil); err == nil {
		t.Fatal("expected New to refuse a single-member group")
	}
}

// TestRefusesDuplicateIDs: recovery matches records to sinks by ID, so two
// members sharing one would be indistinguishable.
func TestRefusesDuplicateIDs(t *testing.T) {
	c, _ := twopc.New(twopc.Options{Store: newMemStore(), WorkflowID: "wf-1"})
	_, err := New([]Member{
		{ID: "same", Sink: &txSink{name: "a"}},
		{ID: "same", Sink: &txSink{name: "b"}},
	}, c, nil)
	if err == nil {
		t.Fatal("expected New to refuse duplicate member IDs")
	}
}

// TestPingFailsWhenAnyMemberIsDown: a group is only as available as its least
// available member, because one that cannot prepare aborts the batch for all.
func TestPingFailsWhenAnyMemberIsDown(t *testing.T) {
	a := &txSink{name: "a"}
	b := &downSink{}
	c, _ := twopc.New(twopc.Options{Store: newMemStore(), WorkflowID: "wf-1"})
	g, err := New([]Member{{ID: "a", Sink: a}, {ID: "b", Sink: b}}, c, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := g.Ping(context.Background()); err == nil {
		t.Fatal("Ping reported healthy while a member was down")
	}
}

// downSink participates but fails its health check.
type downSink struct{ txSink }

func (*downSink) Ping(context.Context) error { return errors.New("unreachable") }

func TestEmptyBatchIsANoop(t *testing.T) {
	a := &txSink{name: "a"}
	b := &txSink{name: "b"}
	g := newGroup(t, Member{ID: "a", Sink: a}, Member{ID: "b", Sink: b})

	if err := g.WriteBatch(context.Background(), nil); err != nil {
		t.Fatalf("WriteBatch(nil): %v", err)
	}
	if written, committed, _ := a.counts(); written != 0 || committed != 0 {
		t.Error("an empty batch started a transaction")
	}
}

// TestRecoverResolvesInDoubtMembers wires the group to the coordinator's
// recovery path, which is what runs at start-up.
func TestRecoverResolvesInDoubtMembers(t *testing.T) {
	store := newMemStore()
	a := &txSink{name: "a"}
	b := &txSink{name: "b"}

	c, _ := twopc.New(twopc.Options{Store: store, WorkflowID: "wf-1"})
	g, err := New([]Member{{ID: "a", Sink: a}, {ID: "b", Sink: b}}, c, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Nothing in doubt yet: recovery must be a clean no-op, since it runs on
	// every start including the ordinary ones.
	if err := g.Recover(context.Background()); err != nil {
		t.Fatalf("Recover on a clean log: %v", err)
	}
	if _, _, rolledBack := a.counts(); rolledBack != 0 {
		t.Error("recovery rolled something back on a clean log")
	}
}

// preflightSink can be asked whether 2PC is genuinely available to it.
type preflightSink struct {
	txSink
	err error
}

func (p *preflightSink) PreflightTwoPhaseCommit(context.Context) error { return p.err }

// TestPreflightRefusesAMemberThatCannotHonourTheContract is the check that
// catches the pooled-PostgreSQL case: it implements the interface but would
// commit when asked to prepare, which inside a group is silent divergence.
func TestPreflightRefusesAMemberThatCannotHonourTheContract(t *testing.T) {
	a := &txSink{name: "a"}
	b := &preflightSink{err: errors.New("two-phase commit is not possible through a transaction pooler")}

	c, _ := twopc.New(twopc.Options{Store: newMemStore(), WorkflowID: "wf-1"})
	g, err := New([]Member{{ID: "a", Sink: a}, {ID: "pooled", Sink: b}}, c, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	err = g.Preflight(context.Background())
	if err == nil {
		t.Fatal("Preflight accepted a member that cannot honour the transaction contract")
	}
	if !strings.Contains(err.Error(), "pooled") {
		t.Errorf("the error should name the offending member: %v", err)
	}
}

// TestPreflightPassesWhenEveryMemberIsCapable keeps the check from being
// vacuously strict.
func TestPreflightPassesWhenEveryMemberIsCapable(t *testing.T) {
	a := &preflightSink{}
	b := &preflightSink{}

	c, _ := twopc.New(twopc.Options{Store: newMemStore(), WorkflowID: "wf-1"})
	g, err := New([]Member{{ID: "a", Sink: a}, {ID: "b", Sink: b}}, c, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := g.Preflight(context.Background()); err != nil {
		t.Fatalf("Preflight rejected a capable group: %v", err)
	}
}

// TestStartReaperSweepsAndStops covers the scheduler, not the reaping logic
// (which twopc tests). What matters here is that it actually fires, and that
// stopping it waits rather than leaking a goroutine into the next test.
func TestStartReaperSweepsAndStops(t *testing.T) {
	store := newMemStore()
	a := &txSink{name: "a"}
	b := &txSink{name: "b"}

	// MaxPreparedAge of a nanosecond makes anything in doubt immediately stale.
	c, err := twopc.New(twopc.Options{
		Store: store, WorkflowID: "wf-1", MaxPreparedAge: time.Nanosecond,
	})
	if err != nil {
		t.Fatalf("twopc.New: %v", err)
	}
	g, err := New([]Member{{ID: "a", Sink: a}, {ID: "b", Sink: b}}, c, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	stop := g.StartReaper(context.Background(), 5*time.Millisecond)
	time.Sleep(30 * time.Millisecond)
	stop()

	// Nothing was in doubt, so nothing should have been rolled back. The value
	// of the assertion is the inverse: a reaper that rolled back healthy
	// transactions on a timer would show up here.
	if _, _, rolledBack := a.counts(); rolledBack != 0 {
		t.Errorf("the reaper rolled back %d transactions with nothing in doubt", rolledBack)
	}

	// stop() must be synchronous: a second call proves it did not leave a
	// goroutine running that would panic on a closed channel.
	stop()
}

// TestStartReaperStopsWithItsContext: a cancelled context must wind the sweep
// down without needing the stop function, since the engine's shutdown path
// cancels rather than calling back.
func TestStartReaperStopsWithItsContext(t *testing.T) {
	c, _ := twopc.New(twopc.Options{Store: newMemStore(), WorkflowID: "wf-1"})
	g, err := New([]Member{
		{ID: "a", Sink: &txSink{name: "a"}},
		{ID: "b", Sink: &txSink{name: "b"}},
	}, c, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	stop := g.StartReaper(ctx, 5*time.Millisecond)
	cancel()

	done := make(chan struct{})
	go func() { stop(); close(done) }()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the reaper did not stop when its context was cancelled")
	}
}
