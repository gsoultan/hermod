package twopc

import (
	"context"
	"strings"
	"sync"
	"testing"

	hermod "github.com/gsoultan/Hermod"
)

// The coordinator must be able to name any transaction a participant might
// hold.
//
// A participant used to name its own transaction and return the name, which
// left a window: a crash between Prepare returning and the coordinator writing
// that name down produced a prepared transaction nothing could name. On
// PostgreSQL a prepared transaction holds its locks cluster-wide until somebody
// finds it by hand in pg_prepared_xacts, so an orphan is an outage rather than
// a tidy-up. README called this the residual risk and said what would close it:
// the participant accepting a coordinator-supplied ID.
//
// The property that closes it is an ordering one, so the tests assert ordering:
// by the time a participant is asked to prepare, the name it will use is
// already durable.

// observingParticipant reports what the store held at the moment it was asked
// to prepare — which is the only way to see the ordering from the outside.
type observingParticipant struct {
	mu sync.Mutex

	store           *memStore
	name            string
	gotTxID         string
	storedAtPrepare []byte
}

func (p *observingParticipant) Prepare(_ context.Context, txID string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.gotTxID = txID
	// Snapshot every record the coordinator has written so far.
	p.store.mu.Lock()
	for _, v := range p.store.kv {
		p.storedAtPrepare = append(p.storedAtPrepare, v...)
	}
	p.store.mu.Unlock()
	return txID, nil
}

func (p *observingParticipant) CommitPrepared(context.Context, string) error   { return nil }
func (p *observingParticipant) RollbackPrepared(context.Context, string) error { return nil }
func (p *observingParticipant) Begin(context.Context) error                    { return nil }
func (p *observingParticipant) Commit(context.Context) error                   { return nil }
func (p *observingParticipant) Rollback(context.Context) error                 { return nil }
func (p *observingParticipant) Write(context.Context, hermod.Message) error    { return nil }
func (p *observingParticipant) Ping(context.Context) error                     { return nil }
func (p *observingParticipant) Close() error                                   { return nil }

// The name is durable before the participant is asked to use it.
func TestTheTransactionIsNamedBeforeItCanExist(t *testing.T) {
	store := newMemStore()
	c := newTestCoordinator(t, store)

	// Two, because a single-participant transaction is refused: it already
	// gets atomicity from its own transaction and needs no coordinator.
	p := &observingParticipant{store: store, name: "a"}
	other := &fakeParticipant{name: "b"}

	if err := c.Run(context.Background(), []Participant{{ID: "a", Sink: p}, {ID: "b", Sink: other}},
		func(context.Context) error { return nil }); err != nil {
		t.Fatalf("Run: %v", err)
	}

	p.mu.Lock()
	got, seen := p.gotTxID, string(p.storedAtPrepare)
	p.mu.Unlock()

	if got == "" {
		t.Fatal("the participant was asked to prepare without a transaction ID; it would " +
			"have to name its own, which is the window this closes")
	}
	if !strings.Contains(seen, got) {
		t.Errorf("the coordinator asked for a prepare under %q before that ID was in the "+
			"store\nwhat was durable at that moment: %s\n\n"+
			"a crash here leaves a prepared transaction nothing can name, and on "+
			"PostgreSQL it holds locks cluster-wide until somebody finds it by hand",
			got, seen)
	}
}

// A participant that reports a different ID than the one supplied — the
// PostgreSQL sink does this behind a pooler, where it commits locally instead —
// has that reported ID recorded, because that is what recovery must act on.
type renamingParticipant struct{ observingParticipant }

func (p *renamingParticipant) Prepare(ctx context.Context, txID string) (string, error) {
	if _, err := p.observingParticipant.Prepare(ctx, txID); err != nil {
		return "", err
	}
	return "local-commit", nil
}

func TestAParticipantThatReportsADifferentIDHasItRecorded(t *testing.T) {
	store := newMemStore()
	c := newTestCoordinator(t, store)

	p := &renamingParticipant{observingParticipant{store: store, name: "a"}}
	other := &observingParticipant{store: store, name: "b"}

	if err := c.Run(context.Background(), []Participant{{ID: "a", Sink: p}, {ID: "b", Sink: other}},
		func(context.Context) error { return nil }); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// The record is deleted once the transaction commits, so what matters is
	// what was durable *while* it was in flight. The second participant sees
	// the store at its own prepare, which is after the first has reported its
	// substitute identifier.
	other.mu.Lock()
	seen := string(other.storedAtPrepare)
	other.mu.Unlock()

	if !strings.Contains(seen, "local-commit") {
		t.Errorf("the participant reported a different transaction ID and the coordinator "+
			"had not recorded it by the time the next participant prepared; recovery "+
			"would act on an identifier the participant is not holding\nstore held: %s", seen)
	}
}
