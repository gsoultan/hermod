package twopc

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	hermod "github.com/gsoultan/Hermod"
)

// ---------------------------------------------------------------------------
// Two-phase commit.
//
// The point of 2PC is a single guarantee: either every participant applied the
// batch or none did. Everything here exists to hold that guarantee across a
// crash, because a coordinator that only works while the process is alive is
// not a coordinator — it is an ordering convention.
//
// The hard cases are all recovery cases. A participant that has PREPAREd is
// "in doubt": it holds its changes and its locks, and only the coordinator's
// log can say whether to commit or abort. Get that wrong and you either lose
// the batch or, on PostgreSQL, leave a prepared transaction pinning locks and
// blocking VACUUM until a human intervenes.
//
// This suite fixes the semantics as **presumed abort**: a transaction is only
// committed if the log says the decision was reached. Anything else — a crash
// mid-prepare, a crash after preparing but before deciding — aborts. That
// direction is deliberate. Committing on doubt would apply a batch the
// coordinator never agreed to.
// ---------------------------------------------------------------------------

// fakeParticipant is a hermod.TwoPhaseCommit whose every step can be made to
// fail, and which records what it was asked to do.
type fakeParticipant struct {
	mu sync.Mutex

	name string

	prepareErr  error
	commitErr   error
	rollbackErr error

	prepared   int
	committed  int
	rolledBack int

	// lastTxID is what Prepare handed back, i.e. what the coordinator must use
	// when it later resolves this participant.
	lastTxID string
	// resolvedWith records the txID presented to CommitPrepared/RollbackPrepared.
	resolvedWith string
}

func (p *fakeParticipant) Begin(context.Context) error  { return nil }
func (p *fakeParticipant) Commit(context.Context) error { return nil }

func (p *fakeParticipant) Rollback(context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.rolledBack++
	return p.rollbackErr
}

// Prepare honours the coordinator-supplied ID, which is what a real
// participant now does: the coordinator records the name before the
// transaction exists, so returning a different one would defeat the point.
func (p *fakeParticipant) Prepare(_ context.Context, txID string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.prepareErr != nil {
		return "", p.prepareErr
	}
	p.prepared++
	p.lastTxID = txID
	return txID, nil
}

func (p *fakeParticipant) CommitPrepared(_ context.Context, txID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.resolvedWith = txID
	if p.commitErr != nil {
		return p.commitErr
	}
	p.committed++
	return nil
}

func (p *fakeParticipant) RollbackPrepared(_ context.Context, txID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.resolvedWith = txID
	if p.rollbackErr != nil {
		return p.rollbackErr
	}
	p.rolledBack++
	return nil
}

func (p *fakeParticipant) counts() (prepared, committed, rolledBack int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.prepared, p.committed, p.rolledBack
}

// memStore is an in-memory hermod.StateStore. It is deliberately not a mock:
// recovery is tested by building a *second* coordinator over the same store,
// which is exactly what a restart does.
type memStore struct {
	mu sync.Mutex
	kv map[string][]byte
	// failWrites, when set, makes Set fail — used to prove the coordinator
	// refuses to prepare when it cannot record its intent.
	failWrites error
}

func newMemStore() *memStore { return &memStore{kv: map[string][]byte{}} }

func (m *memStore) Get(_ context.Context, key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.kv[key]
	if !ok {
		return nil, ErrNotFound
	}
	return v, nil
}

func (m *memStore) Set(_ context.Context, key string, value []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failWrites != nil {
		return m.failWrites
	}
	m.kv[key] = value
	return nil
}

func (m *memStore) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.kv, key)
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

func newTestCoordinator(t *testing.T, store *memStore) *Coordinator {
	t.Helper()
	c, err := New(Options{Store: store, WorkflowID: "wf-1"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

// TestCommitsEveryParticipant is the happy path: prepare all, then commit all.
func TestCommitsEveryParticipant(t *testing.T) {
	store := newMemStore()
	c := newTestCoordinator(t, store)

	a := &fakeParticipant{name: "a"}
	b := &fakeParticipant{name: "b"}

	if err := c.Run(context.Background(), []Participant{
		{ID: "a", Sink: a}, {ID: "b", Sink: b},
	}, func(context.Context) error { return nil }); err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, p := range []*fakeParticipant{a, b} {
		prepared, committed, rolledBack := p.counts()
		if prepared != 1 || committed != 1 || rolledBack != 0 {
			t.Errorf("%s: prepared=%d committed=%d rolledBack=%d, want 1/1/0",
				p.name, prepared, committed, rolledBack)
		}
	}
}

// TestResolvesWithTheTxIDPrepareReturned catches the subtlest way to break 2PC:
// resolving a participant with an identifier it never issued. PostgreSQL keys
// COMMIT PREPARED on the gid, so a mismatch silently leaves the transaction in
// doubt while the coordinator believes it is done.
func TestResolvesWithTheTxIDPrepareReturned(t *testing.T) {
	c := newTestCoordinator(t, newMemStore())
	a := &fakeParticipant{name: "a"}
	b := &fakeParticipant{name: "b"}

	if err := c.Run(context.Background(), []Participant{{ID: "a", Sink: a}, {ID: "b", Sink: b}},
		func(context.Context) error { return nil }); err != nil {
		t.Fatalf("Run: %v", err)
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.resolvedWith != a.lastTxID {
		t.Errorf("resolved with %q but Prepare returned %q", a.resolvedWith, a.lastTxID)
	}
}

// TestOnePrepareFailureAbortsEveryone is the whole reason for the protocol.
func TestOnePrepareFailureAbortsEveryone(t *testing.T) {
	c := newTestCoordinator(t, newMemStore())

	good := &fakeParticipant{name: "good"}
	bad := &fakeParticipant{name: "bad", prepareErr: errors.New("disk full")}

	err := c.Run(context.Background(), []Participant{
		{ID: "good", Sink: good}, {ID: "bad", Sink: bad},
	}, func(context.Context) error { return nil })
	if err == nil {
		t.Fatal("expected an error when a participant cannot prepare")
	}

	_, committed, rolledBack := good.counts()
	if committed != 0 {
		t.Errorf("a participant committed while another could not prepare — the batch is now half-applied")
	}
	if rolledBack != 1 {
		t.Errorf("the prepared participant was not rolled back (rolledBack=%d); it is left in doubt holding locks", rolledBack)
	}
}

// TestWorkFailureAbortsBeforePreparing: if producing the batch fails there is
// nothing to commit, and no participant should have been asked to prepare.
func TestWorkFailureAbortsBeforePreparing(t *testing.T) {
	c := newTestCoordinator(t, newMemStore())
	a := &fakeParticipant{name: "a"}
	b := &fakeParticipant{name: "b"}

	err := c.Run(context.Background(), []Participant{{ID: "a", Sink: a}, {ID: "b", Sink: b}},
		func(context.Context) error { return errors.New("write failed") })
	if err == nil {
		t.Fatal("expected the work error to propagate")
	}
	if errors.Is(err, errTooFewParticipants) {
		t.Fatal("failed on participant count, not the work error — the assertion below would be meaningless")
	}

	prepared, committed, _ := a.counts()
	if prepared != 0 || committed != 0 {
		t.Errorf("prepared=%d committed=%d; nothing should be prepared when the write failed", prepared, committed)
	}
}

// TestRefusesToPrepareWhenItCannotLog is the durability precondition. Preparing
// without a durable record is the one truly unrecoverable state: participants
// hold locks and nothing knows they exist.
func TestRefusesToPrepareWhenItCannotLog(t *testing.T) {
	store := newMemStore()
	store.failWrites = errors.New("store unavailable")
	c := newTestCoordinator(t, store)

	a := &fakeParticipant{name: "a"}
	b := &fakeParticipant{name: "b"}
	err := c.Run(context.Background(), []Participant{{ID: "a", Sink: a}, {ID: "b", Sink: b}},
		func(context.Context) error { return nil })
	if err == nil {
		t.Fatal("expected Run to fail when the transaction log cannot be written")
	}
	if errors.Is(err, errTooFewParticipants) {
		t.Fatal("failed on participant count, not the log write")
	}

	if prepared, _, _ := a.counts(); prepared != 0 {
		t.Errorf("prepared %d participant(s) without a durable record; a crash now orphans them", prepared)
	}
}

// TestRecoveryAbortsWhenNoDecisionWasLogged is presumed abort. The coordinator
// crashed after preparing but before deciding, so the batch must not be applied.
func TestRecoveryAbortsWhenNoDecisionWasLogged(t *testing.T) {
	store := newMemStore()
	a := &fakeParticipant{name: "a"}

	// Simulate the crash: drive the protocol only as far as PREPARED.
	first := newTestCoordinator(t, store)
	txID, err := first.prepareOnly(context.Background(), []Participant{{ID: "a", Sink: a}},
		func(context.Context) error { return nil })
	if err != nil {
		t.Fatalf("prepareOnly: %v", err)
	}
	if prepared, _, _ := a.counts(); prepared != 1 {
		t.Fatalf("participant was not prepared, nothing to recover")
	}

	// A new coordinator over the same log is what a restart looks like.
	second := newTestCoordinator(t, store)
	if err := second.Recover(context.Background(), map[string]hermod.TwoPhaseCommit{"a": a}); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	_, committed, rolledBack := a.counts()
	if committed != 0 {
		t.Errorf("recovery committed a transaction whose decision was never logged")
	}
	if rolledBack != 1 {
		t.Errorf("recovery left the participant in doubt (rolledBack=%d)", rolledBack)
	}
	if _, err := store.Get(context.Background(), txID); err == nil {
		t.Error("the log record survived recovery; it will be replayed forever")
	}
}

// TestRecoveryCommitsWhenTheDecisionWasLogged is the other half. Once the
// coordinator has durably decided to commit, that decision binds after a crash
// — otherwise a participant that already committed diverges from one that did
// not.
func TestRecoveryCommitsWhenTheDecisionWasLogged(t *testing.T) {
	store := newMemStore()
	a := &fakeParticipant{name: "a"}

	first := newTestCoordinator(t, store)
	if _, err := first.prepareAndDecide(context.Background(), []Participant{{ID: "a", Sink: a}},
		func(context.Context) error { return nil }); err != nil {
		t.Fatalf("prepareAndDecide: %v", err)
	}

	second := newTestCoordinator(t, store)
	if err := second.Recover(context.Background(), map[string]hermod.TwoPhaseCommit{"a": a}); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	_, committed, rolledBack := a.counts()
	if committed != 1 {
		t.Errorf("recovery did not honour a logged commit decision (committed=%d)", committed)
	}
	if rolledBack != 0 {
		t.Errorf("recovery rolled back a transaction it had decided to commit")
	}
}

// TestRecoveryIsIdempotent: recovery runs on every start, and a participant
// that has already been resolved must not be resolved again.
func TestRecoveryIsIdempotent(t *testing.T) {
	store := newMemStore()
	a := &fakeParticipant{name: "a"}

	first := newTestCoordinator(t, store)
	if _, err := first.prepareOnly(context.Background(), []Participant{{ID: "a", Sink: a}},
		func(context.Context) error { return nil }); err != nil {
		t.Fatalf("prepareOnly: %v", err)
	}

	second := newTestCoordinator(t, store)
	for range 3 {
		if err := second.Recover(context.Background(), map[string]hermod.TwoPhaseCommit{"a": a}); err != nil {
			t.Fatalf("Recover: %v", err)
		}
	}

	if _, _, rolledBack := a.counts(); rolledBack != 1 {
		t.Errorf("rolledBack=%d after three recoveries, want 1", rolledBack)
	}
}

// TestRecoverySkipsUnknownParticipant guards the upgrade case: a sink removed
// from the workflow while a transaction was in doubt. The record must survive
// so an operator can still resolve it, rather than being dropped silently.
func TestRecoverySkipsUnknownParticipant(t *testing.T) {
	store := newMemStore()
	a := &fakeParticipant{name: "a"}

	first := newTestCoordinator(t, store)
	txID, err := first.prepareOnly(context.Background(), []Participant{{ID: "a", Sink: a}},
		func(context.Context) error { return nil })
	if err != nil {
		t.Fatalf("prepareOnly: %v", err)
	}

	second := newTestCoordinator(t, store)
	// Recover with no participants registered at all.
	err = second.Recover(context.Background(), map[string]hermod.TwoPhaseCommit{})
	if err == nil {
		t.Fatal("expected recovery to report that an in-doubt transaction could not be resolved")
	}
	if _, getErr := store.Get(context.Background(), txID); getErr != nil {
		t.Error("the unresolvable record was deleted; the participant is stranded with no trace")
	}
}

// TestReapsPreparedTransactionsPastTheirDeadline is the PostgreSQL safety
// valve. A prepared transaction holds its locks and blocks VACUUM until it is
// resolved, so an in-doubt record that nobody resolves is an outage in waiting.
func TestReapsPreparedTransactionsPastTheirDeadline(t *testing.T) {
	store := newMemStore()
	a := &fakeParticipant{name: "a"}

	c, err := New(Options{Store: store, WorkflowID: "wf-1", MaxPreparedAge: time.Nanosecond})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.prepareOnly(context.Background(), []Participant{{ID: "a", Sink: a}},
		func(context.Context) error { return nil }); err != nil {
		t.Fatalf("prepareOnly: %v", err)
	}

	time.Sleep(2 * time.Millisecond)

	reaped, err := c.Reap(context.Background(), map[string]hermod.TwoPhaseCommit{"a": a})
	if err != nil {
		t.Fatalf("Reap: %v", err)
	}
	if reaped != 1 {
		t.Errorf("reaped %d, want 1", reaped)
	}
	if _, _, rolledBack := a.counts(); rolledBack != 1 {
		t.Errorf("the stale prepared transaction was not rolled back; it still holds locks")
	}
}

// TestReapLeavesFreshTransactionsAlone: reaping an in-flight transaction would
// abort work that is about to commit.
func TestReapLeavesFreshTransactionsAlone(t *testing.T) {
	store := newMemStore()
	a := &fakeParticipant{name: "a"}

	c, err := New(Options{Store: store, WorkflowID: "wf-1", MaxPreparedAge: time.Hour})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.prepareOnly(context.Background(), []Participant{{ID: "a", Sink: a}},
		func(context.Context) error { return nil }); err != nil {
		t.Fatalf("prepareOnly: %v", err)
	}

	reaped, err := c.Reap(context.Background(), map[string]hermod.TwoPhaseCommit{"a": a})
	if err != nil {
		t.Fatalf("Reap: %v", err)
	}
	if reaped != 0 {
		t.Errorf("reaped %d in-flight transaction(s); that aborts live work", reaped)
	}
}

// TestRejectsSingleParticipant: 2PC across one participant is a local
// transaction with extra round trips and a durable log to maintain. Refusing it
// keeps callers from paying for a guarantee they already had.
func TestRejectsSingleParticipant(t *testing.T) {
	c := newTestCoordinator(t, newMemStore())
	err := c.Run(context.Background(), []Participant{{ID: "a", Sink: &fakeParticipant{name: "a"}}},
		func(context.Context) error { return nil })
	if err == nil {
		t.Fatal("expected Run to reject a single-participant transaction")
	}
}

// TestRequiresAStore: without durable storage there is no recovery, and a
// coordinator that cannot recover must not be constructible.
func TestRequiresAStore(t *testing.T) {
	if _, err := New(Options{WorkflowID: "wf-1"}); err == nil {
		t.Fatal("expected New to reject a nil store")
	}
}

// TestOpensATransactionBeforeWriting pins the begin phase.
//
// It was missing at first and no fake caught it: they accepted Prepare
// unconditionally, while a real PostgreSQL sink refuses with "no active
// transaction". The protocol is Begin -> write -> Prepare, and the first step
// is only observable if something asserts on it.
func TestOpensATransactionBeforeWriting(t *testing.T) {
	c := newTestCoordinator(t, newMemStore())

	a := &orderedParticipant{name: "a"}
	b := &orderedParticipant{name: "b"}

	if err := c.Run(context.Background(), []Participant{{ID: "a", Sink: a}, {ID: "b", Sink: b}},
		func(context.Context) error {
			a.note("work")
			b.note("work")
			return nil
		}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, p := range []*orderedParticipant{a, b} {
		got := p.steps()
		want := []string{"begin", "work", "prepare", "commitPrepared"}
		if len(got) != len(want) {
			t.Fatalf("%s: steps %v, want %v", p.name, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%s: step %d was %q, want %q (full: %v)", p.name, i, got[i], want[i], got)
			}
		}
	}
}

// TestBeginFailureTouchesNothing: if a participant cannot open a transaction,
// no write may have happened anywhere.
func TestBeginFailureTouchesNothing(t *testing.T) {
	c := newTestCoordinator(t, newMemStore())

	a := &orderedParticipant{name: "a"}
	b := &orderedParticipant{name: "b", beginErr: errors.New("connection refused")}

	worked := false
	err := c.Run(context.Background(), []Participant{{ID: "a", Sink: a}, {ID: "b", Sink: b}},
		func(context.Context) error { worked = true; return nil })
	if err == nil {
		t.Fatal("expected Run to fail when a participant cannot begin")
	}
	if worked {
		t.Error("the write ran even though a participant could not open a transaction")
	}
	if prepared, _, _ := a.counts(); prepared != 0 {
		t.Error("a participant was prepared after a failed begin")
	}
}

// orderedParticipant records the sequence of protocol steps it was asked for.
type orderedParticipant struct {
	fakeParticipant
	beginErr error

	stepMu sync.Mutex
	seq    []string
}

func (p *orderedParticipant) note(step string) {
	p.stepMu.Lock()
	defer p.stepMu.Unlock()
	p.seq = append(p.seq, step)
}

func (p *orderedParticipant) steps() []string {
	p.stepMu.Lock()
	defer p.stepMu.Unlock()
	return append([]string(nil), p.seq...)
}

func (p *orderedParticipant) Begin(ctx context.Context) error {
	if p.beginErr != nil {
		return p.beginErr
	}
	p.note("begin")
	return p.fakeParticipant.Begin(ctx)
}

func (p *orderedParticipant) Prepare(ctx context.Context, txID string) (string, error) {
	p.note("prepare")
	return p.fakeParticipant.Prepare(ctx, txID)
}

func (p *orderedParticipant) CommitPrepared(ctx context.Context, txID string) error {
	p.note("commitPrepared")
	return p.fakeParticipant.CommitPrepared(ctx, txID)
}

func (p *orderedParticipant) RollbackPrepared(ctx context.Context, txID string) error {
	p.note("rollbackPrepared")
	return p.fakeParticipant.RollbackPrepared(ctx, txID)
}
