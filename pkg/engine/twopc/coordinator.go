// Package twopc coordinates two-phase commit across sinks.
//
// The guarantee is that either every participant applied a batch or none did.
// Everything here exists to hold that across a crash, because a coordinator
// that only works while the process is alive is not a coordinator — it is an
// ordering convention.
//
// # Semantics
//
// Presumed abort. A transaction is committed on recovery only if the log says
// the decision was reached; anything else aborts. Committing on doubt would
// apply a batch the coordinator never agreed to.
//
// # The hazard this package is mostly about
//
// A participant that has prepared is *in doubt*: it holds its changes and its
// locks until someone resolves it. On PostgreSQL a prepared transaction also
// blocks VACUUM for the whole cluster and survives restarts — an unresolved one
// is an outage in waiting, not an untidy row. So:
//
//   - nothing is prepared before the intent is durable (Run refuses otherwise);
//   - Recover resolves in-doubt transactions on every start; and
//   - Reap rolls back anything that has been prepared for too long.
//
// Read the "Operational hazard" section of README.md before enabling this.
package twopc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/user/hermod"
)

// ErrNotFound is what a Store returns for a missing key.
var ErrNotFound = errors.New("twopc: record not found")

// errTooFewParticipants is returned when fewer than two participants are given.
// It is a sentinel so tests can tell "rejected on arity" apart from "rejected
// for the reason under test" — several assertions here would otherwise pass for
// the wrong reason.
var errTooFewParticipants = errors.New("twopc: too few participants")

// Store is the coordinator's durable log.
//
// It is hermod.StateStore plus List, because recovery has to enumerate what was
// in flight and the base interface cannot. Wrap a plain StateStore with
// NewIndexedStore to get one.
type Store interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Set(ctx context.Context, key string, value []byte) error
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, prefix string) (map[string][]byte, error)
}

// Participant is one sink taking part in a transaction. ID must be stable
// across restarts: recovery matches log records to live sinks by it.
type Participant struct {
	ID   string
	Sink hermod.TwoPhaseCommit
}

// state is the coordinator's view of a transaction. The order matters: the
// decision to commit is durable before any participant is told about it.
type state string

const (
	// statePreparing: intent recorded, participants not yet asked to vote.
	statePreparing state = "preparing"
	// statePrepared: every participant voted yes. NO DECISION YET — recovery
	// from here aborts.
	statePrepared state = "prepared"
	// stateCommitting: the decision is durable. Recovery from here commits,
	// however many times it takes.
	stateCommitting state = "committing"
	// stateAborting: the decision to abort is durable.
	stateAborting state = "aborting"
)

// record is one transaction's log entry.
type record struct {
	TxID       string    `json:"tx_id"`
	WorkflowID string    `json:"workflow_id"`
	State      state     `json:"state"`
	StartedAt  time.Time `json:"started_at"`
	// PreparedAt is when the last participant voted, i.e. when the clock on the
	// in-doubt window starts. Reap measures against this.
	PreparedAt time.Time `json:"prepared_at,omitzero"`
	// Order is the participant IDs in a fixed sequence, so resolution is
	// deterministic and a partially-resolved transaction replays the same way.
	Order []string `json:"order"`
	// TxIDs maps participant ID to the identifier that participant's Prepare
	// returned. Resolving with anything else silently leaves it in doubt.
	TxIDs map[string]string `json:"tx_ids"`
}

// DefaultMaxPreparedAge bounds how long a transaction may sit in doubt before
// Reap rolls it back. Generous enough to survive a slow commit round, short
// enough that a forgotten prepared transaction does not quietly wreck
// autovacuum.
const DefaultMaxPreparedAge = 15 * time.Minute

// Options configures a Coordinator.
type Options struct {
	// Store is the durable log. Required: without it there is no recovery, and
	// a coordinator that cannot recover is worse than none.
	Store Store
	// WorkflowID scopes log keys so coordinators do not see each other's
	// transactions.
	WorkflowID string
	// MaxPreparedAge overrides DefaultMaxPreparedAge.
	MaxPreparedAge time.Duration
	// Logger is optional.
	Logger hermod.Logger
	// Now is injectable for tests.
	Now func() time.Time
}

// Coordinator drives two-phase commit over a set of participants.
type Coordinator struct {
	store          Store
	workflowID     string
	maxPreparedAge time.Duration
	logger         hermod.Logger
	now            func() time.Time

	// mu serialises transactions. 2PC is a barrier by nature — overlapping
	// transactions across the same participants would interleave prepares and
	// make the log ambiguous.
	mu sync.Mutex
}

// New builds a Coordinator.
func New(opts Options) (*Coordinator, error) {
	if opts.Store == nil {
		return nil, errors.New("twopc: a durable Store is required; without one, a crash leaves participants in doubt with nothing to resolve them")
	}
	if opts.WorkflowID == "" {
		return nil, errors.New("twopc: WorkflowID is required to scope the transaction log")
	}
	maxAge := opts.MaxPreparedAge
	if maxAge <= 0 {
		maxAge = DefaultMaxPreparedAge
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Coordinator{
		store:          opts.Store,
		workflowID:     opts.WorkflowID,
		maxPreparedAge: maxAge,
		logger:         opts.Logger,
		now:            now,
	}, nil
}

// keyPrefix scopes this coordinator's records.
func (c *Coordinator) keyPrefix() string { return "twopc/" + c.workflowID + "/" }

func (c *Coordinator) key(txID string) string { return c.keyPrefix() + txID }

func (c *Coordinator) log(level, msg string, kv ...any) {
	if c.logger == nil {
		return
	}
	switch level {
	case "warn":
		c.logger.Warn(msg, kv...)
	case "error":
		c.logger.Error(msg, kv...)
	default:
		c.logger.Info(msg, kv...)
	}
}

// Run executes work inside a distributed transaction across participants.
//
// work performs the actual writes; it runs *before* the vote, because a
// participant can only prepare changes it already has. If work fails, nothing
// is prepared and every participant is rolled back.
//
// Run is safe to call concurrently but serialises internally.
func (c *Coordinator) Run(ctx context.Context, participants []Participant, work func(context.Context) error) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(participants) < 2 {
		return fmt.Errorf("%w: got %d, a distributed transaction needs at least two, "+
			"and a single participant already gets atomicity from its own transaction",
			errTooFewParticipants, len(participants))
	}
	for _, p := range participants {
		if p.Sink == nil {
			return fmt.Errorf("twopc: participant %q has no sink", p.ID)
		}
		if p.ID == "" {
			return errors.New("twopc: every participant needs a stable ID; recovery matches log records to sinks by it")
		}
	}

	rec, err := c.prepareAll(ctx, participants, work)
	if err != nil {
		return err
	}
	return c.commitAll(ctx, participants, rec)
}

// prepareAll runs work, records intent, and collects votes. On any failure it
// rolls back whatever it touched and returns the error.
func (c *Coordinator) prepareAll(ctx context.Context, participants []Participant, work func(context.Context) error) (*record, error) {
	// Open a transaction on every participant before anything is written.
	//
	// This is not ceremony: a participant can only prepare work that is already
	// inside a transaction it owns. PostgresSink, for one, refuses to prepare
	// with "no active transaction" — so a coordinator that skipped this would
	// fail on the first real batch while passing every test whose fake accepts
	// Prepare unconditionally.
	for i, p := range participants {
		if err := p.Sink.Begin(ctx); err != nil {
			// Roll back the ones already open; the rest were never touched.
			c.rollbackOpen(ctx, participants[:i])
			return nil, fmt.Errorf("twopc: participant %q could not begin: %w", p.ID, err)
		}
	}

	// The writes happen next: a participant prepares changes it already holds.
	if err := work(ctx); err != nil {
		c.rollbackOpen(ctx, participants)
		return nil, fmt.Errorf("twopc: write failed, transaction abandoned: %w", err)
	}

	rec := &record{
		TxID:       uuid.NewString(),
		WorkflowID: c.workflowID,
		State:      statePreparing,
		StartedAt:  c.now(),
		TxIDs:      map[string]string{},
	}
	for _, p := range participants {
		rec.Order = append(rec.Order, p.ID)
	}

	// Durable intent BEFORE the first Prepare. If this write fails the
	// transaction must not proceed: preparing without a record is the one
	// unrecoverable state, because the participants would hold locks that
	// nothing knows to release.
	if err := c.save(ctx, rec); err != nil {
		c.rollbackOpen(ctx, participants)
		return nil, fmt.Errorf("twopc: cannot record transaction intent, refusing to prepare: %w", err)
	}

	for _, p := range participants {
		// The name is chosen and recorded here, before the participant is asked
		// to prepare anything. Previously the participant named its own
		// transaction and returned the name, which left a window: a crash
		// between Prepare returning and the coordinator writing that name down
		// produced a prepared transaction nothing could name. On PostgreSQL
		// that transaction holds its locks cluster-wide until somebody finds it
		// by hand in pg_prepared_xacts.
		//
		// Writing the name first inverts the failure: a crash now leaves at
		// worst a name recorded for a transaction that was never prepared, and
		// recovery rolling back an identifier that does not exist is a no-op.
		// An orphan you can name is a cleanup; one you cannot is an outage.
		txID := c.newTxID()
		rec.TxIDs[p.ID] = txID
		if err := c.save(ctx, rec); err != nil {
			delete(rec.TxIDs, p.ID)
			c.abort(ctx, participants, rec)
			return nil, fmt.Errorf("twopc: cannot record the transaction ID for "+
				"participant %q, refusing to prepare it: %w", p.ID, err)
		}

		actual, err := p.Sink.Prepare(ctx, txID)
		if err != nil {
			c.abort(ctx, participants, rec)
			return nil, fmt.Errorf("twopc: participant %q could not prepare: %w", p.ID, err)
		}

		// A participant may report a different identifier — the PostgreSQL sink
		// returns a sentinel when it is behind a pooler and had to commit
		// locally instead. Record what is actually in force, since that is what
		// recovery will act on.
		if actual != txID {
			rec.TxIDs[p.ID] = actual
			if err := c.save(ctx, rec); err != nil {
				c.abort(ctx, participants, rec)
				return nil, fmt.Errorf("twopc: cannot record the vote of participant %q: %w", p.ID, err)
			}
		}
	}

	rec.State = statePrepared
	rec.PreparedAt = c.now()
	if err := c.save(ctx, rec); err != nil {
		c.abort(ctx, participants, rec)
		return nil, fmt.Errorf("twopc: cannot record the prepared state: %w", err)
	}
	return rec, nil
}

// newTxID names a participant's transaction. The coordinator generates it so it
// can be recorded before the transaction exists; see the loop in prepareAll.
//
// It is a UUID because the PostgreSQL sink interpolates it into PREPARE
// TRANSACTION, which takes a string literal rather than a bind parameter, and
// refuses anything that is not a UUID.
func (c *Coordinator) newTxID() string {
	return uuid.New().String()
}

// commitAll makes the decision durable, then applies it.
func (c *Coordinator) commitAll(ctx context.Context, participants []Participant, rec *record) error {
	// The decision is durable before anyone is told. Recovery reads this and is
	// bound by it: past this line the transaction commits, however many
	// attempts that takes.
	rec.State = stateCommitting
	if err := c.save(ctx, rec); err != nil {
		c.abort(ctx, participants, rec)
		return fmt.Errorf("twopc: cannot record the commit decision: %w", err)
	}

	byID := map[string]hermod.TwoPhaseCommit{}
	for _, p := range participants {
		byID[p.ID] = p.Sink
	}

	if err := c.resolve(ctx, rec, byID, true); err != nil {
		// The decision stands. Leaving the record is deliberate: recovery will
		// finish the commit, and reporting success here would claim a durability
		// the participants do not yet have.
		return fmt.Errorf("twopc: commit incomplete, will be finished by recovery: %w", err)
	}

	return c.store.Delete(ctx, c.key(rec.TxID))
}

// abort records the decision to abort and applies it. Errors are logged rather
// than returned: the caller is already failing, and recovery will retry.
func (c *Coordinator) abort(ctx context.Context, participants []Participant, rec *record) {
	rec.State = stateAborting
	if err := c.save(ctx, rec); err != nil {
		c.log("error", "twopc: could not record abort decision; recovery will presume abort anyway",
			"workflow_id", c.workflowID, "tx_id", rec.TxID, "error", err)
	}

	byID := map[string]hermod.TwoPhaseCommit{}
	for _, p := range participants {
		byID[p.ID] = p.Sink
	}
	if err := c.resolve(ctx, rec, byID, false); err != nil {
		c.log("error", "twopc: abort incomplete, recovery will retry",
			"workflow_id", c.workflowID, "tx_id", rec.TxID, "error", err)
		return
	}
	if err := c.store.Delete(ctx, c.key(rec.TxID)); err != nil {
		c.log("warn", "twopc: could not delete resolved record", "tx_id", rec.TxID, "error", err)
	}
}

// rollbackOpen discards uncommitted work on participants that were never
// prepared. Best effort: there is no in-doubt state to protect yet.
func (c *Coordinator) rollbackOpen(ctx context.Context, participants []Participant) {
	for _, p := range participants {
		if err := p.Sink.Rollback(ctx); err != nil {
			c.log("warn", "twopc: rollback of an unprepared participant failed",
				"workflow_id", c.workflowID, "participant", p.ID, "error", err)
		}
	}
}

// resolve applies the decision to every participant that voted, in the recorded
// order. It removes each participant from rec.TxIDs as it succeeds and persists
// the shrinking record, so a partially-resolved transaction resumes rather than
// double-resolving.
func (c *Coordinator) resolve(ctx context.Context, rec *record, byID map[string]hermod.TwoPhaseCommit, commit bool) error {
	var unresolved []string

	for _, id := range rec.Order {
		txID, voted := rec.TxIDs[id]
		if !voted {
			continue // never prepared, or already resolved
		}

		sink, known := byID[id]
		if !known {
			// The sink is gone from the workflow while its transaction is in
			// doubt. Do not drop the record: it is the only trace an operator
			// has of a transaction still holding locks.
			unresolved = append(unresolved, id)
			continue
		}

		var err error
		if commit {
			err = sink.CommitPrepared(ctx, txID)
		} else {
			err = sink.RollbackPrepared(ctx, txID)
		}
		if err != nil {
			unresolved = append(unresolved, id)
			c.log("error", "twopc: could not resolve participant",
				"workflow_id", c.workflowID, "tx_id", rec.TxID,
				"participant", id, "commit", commit, "error", err)
			continue
		}

		delete(rec.TxIDs, id)
		if serr := c.save(ctx, rec); serr != nil {
			// The participant is resolved but the log still lists it. Recovery
			// would resolve it a second time, which for PostgreSQL is a
			// harmless "no such prepared transaction". Worth a warning, not a
			// failure.
			c.log("warn", "twopc: resolved a participant but could not update the log",
				"tx_id", rec.TxID, "participant", id, "error", serr)
		}
	}

	if len(unresolved) > 0 {
		sort.Strings(unresolved)
		return fmt.Errorf("participants still in doubt: %v", unresolved)
	}
	return nil
}

// Recover resolves every transaction left in doubt by a previous run. Call it
// once at start-up, before the pipeline writes anything.
//
// sinks maps participant ID to the live sink. A record naming a participant
// that is not present cannot be resolved; Recover reports that and leaves the
// record alone rather than deleting evidence of a transaction that still holds
// locks.
func (c *Coordinator) Recover(ctx context.Context, sinks map[string]hermod.TwoPhaseCommit) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	records, err := c.load(ctx)
	if err != nil {
		return fmt.Errorf("twopc: cannot read the transaction log: %w", err)
	}
	if len(records) == 0 {
		return nil
	}

	var failures []string
	for _, rec := range records {
		// Presumed abort: only a durable commit decision commits.
		commit := rec.State == stateCommitting

		c.log("info", "twopc: resolving in-doubt transaction",
			"workflow_id", c.workflowID, "tx_id", rec.TxID,
			"state", string(rec.State), "commit", commit)

		if err := c.resolve(ctx, rec, sinks, commit); err != nil {
			failures = append(failures, fmt.Sprintf("%s (%s): %v", rec.TxID, rec.State, err))
			continue
		}
		if err := c.store.Delete(ctx, c.key(rec.TxID)); err != nil {
			failures = append(failures, fmt.Sprintf("%s: delete: %v", rec.TxID, err))
		}
	}

	if len(failures) > 0 {
		return fmt.Errorf("twopc: %d transaction(s) remain in doubt and are still holding locks: %v",
			len(failures), failures)
	}
	return nil
}

// Reap rolls back transactions that have been prepared for longer than
// MaxPreparedAge, returning how many it resolved.
//
// This is the safety valve for the PostgreSQL hazard: a prepared transaction
// holds its locks and blocks VACUUM cluster-wide until someone resolves it. A
// coordinator that dies mid-round and never comes back would otherwise leave
// that forever. Call it periodically.
//
// It only ever rolls back. Committing on a timer would apply a batch nobody
// decided to commit.
func (c *Coordinator) Reap(ctx context.Context, sinks map[string]hermod.TwoPhaseCommit) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	records, err := c.load(ctx)
	if err != nil {
		return 0, fmt.Errorf("twopc: cannot read the transaction log: %w", err)
	}

	cutoff := c.now().Add(-c.maxPreparedAge)
	reaped := 0

	for _, rec := range records {
		// A transaction whose commit decision is durable is not stale, it is
		// unfinished. Recovery owns it; rolling it back here would contradict a
		// decision already taken.
		if rec.State == stateCommitting {
			continue
		}
		when := rec.PreparedAt
		if when.IsZero() {
			when = rec.StartedAt
		}
		if when.After(cutoff) {
			continue
		}

		c.log("warn", "twopc: rolling back a transaction that has been in doubt too long",
			"workflow_id", c.workflowID, "tx_id", rec.TxID,
			"in_doubt_for", c.now().Sub(when).String(), "limit", c.maxPreparedAge.String())

		if err := c.resolve(ctx, rec, sinks, false); err != nil {
			c.log("error", "twopc: reap could not resolve a stale transaction",
				"tx_id", rec.TxID, "error", err)
			continue
		}
		if err := c.store.Delete(ctx, c.key(rec.TxID)); err != nil {
			c.log("warn", "twopc: could not delete a reaped record", "tx_id", rec.TxID, "error", err)
		}
		reaped++
	}

	return reaped, nil
}

// InDoubt reports the transactions currently awaiting resolution. Intended for
// operators and metrics: a number that does not return to zero means locks are
// being held.
func (c *Coordinator) InDoubt(ctx context.Context) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	records, err := c.load(ctx)
	if err != nil {
		return 0, err
	}
	return len(records), nil
}

func (c *Coordinator) save(ctx context.Context, rec *record) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return c.store.Set(ctx, c.key(rec.TxID), data)
}

// load returns every record for this workflow, ordered by start time so
// resolution is deterministic.
func (c *Coordinator) load(ctx context.Context) ([]*record, error) {
	raw, err := c.store.List(ctx, c.keyPrefix())
	if err != nil {
		return nil, err
	}

	records := make([]*record, 0, len(raw))
	for key, data := range raw {
		var rec record
		if err := json.Unmarshal(data, &rec); err != nil {
			// A record we cannot parse is a record we cannot resolve. Say so
			// loudly and keep it: deleting it would silently strand whatever it
			// described.
			c.log("error", "twopc: unparseable transaction log record; resolve it by hand",
				"key", key, "error", err)
			continue
		}
		records = append(records, &rec)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].StartedAt.Before(records[j].StartedAt) })
	return records, nil
}

// WorkflowID reports the workflow this coordinator's transaction log is scoped
// to. It labels the metrics the reaper publishes, so an in-doubt transaction
// can be traced back to the workflow that left it.
func (c *Coordinator) WorkflowID() string { return c.workflowID }
