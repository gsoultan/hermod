// Package txgroup writes to several sinks inside one distributed transaction.
//
// It exists because Hermod's engine gives every sink its own writer goroutine
// with its own batching loop. That independence is why the engine sustains
// ~100k msgs/s, and it is also why sinks cannot agree on a transaction
// boundary: there is no point in the hot path where one batch is handed to all
// of them together.
//
// So rather than serialising the engine, a group presents itself as a single
// hermod.Sink. The engine drives it through one ordinary writer, and inside
// that writer the group runs two-phase commit across its members. Workflows
// that do not need atomicity keep the fast independent path untouched.
//
// # What it guarantees
//
// Either every member applied the batch or none did — across a crash, because
// the coordinator's decision is durable before any member is told about it.
//
// # What it costs
//
// A group is a barrier. Its members commit in lockstep at the pace of the
// slowest, and a member that cannot prepare aborts the batch for all of them.
// Use it where a partial write is genuinely worse than a slower pipeline.
//
// Read the "Operational hazard" section of README.md before enabling this on
// PostgreSQL: a prepared transaction holds locks and blocks VACUUM until it is
// resolved.
package txgroup

import (
	"context"
	"errors"
	"fmt"
	"sync"

	hermod "github.com/gsoultan/Hermod"
	"github.com/gsoultan/Hermod/pkg/engine/twopc"
)

// Member is one sink in the group.
type Member struct {
	// ID must be stable across restarts: recovery matches log records to sinks
	// by it, so renaming one strands any transaction it was part of.
	ID   string
	Sink hermod.Sink
}

// Sink writes to every member inside one distributed transaction.
type Sink struct {
	members     []Member
	coordinator *twopc.Coordinator
	logger      hermod.Logger

	// mu serialises writes. The coordinator serialises too, but holding it here
	// keeps the member-write phase inside the same critical section, so two
	// batches cannot interleave their writes before either prepares.
	mu sync.Mutex
}

// New builds a group.
//
// Every member must implement hermod.TwoPhaseCommit. This is checked up front
// and refused loudly rather than degraded silently: a group containing one sink
// that cannot participate offers no atomicity at all, and discovering that
// during an incident is the worst possible time.
func New(members []Member, coordinator *twopc.Coordinator, logger hermod.Logger) (*Sink, error) {
	if coordinator == nil {
		return nil, errors.New("txgroup: a coordinator is required")
	}
	if len(members) < 2 {
		return nil, fmt.Errorf("txgroup: %d member(s): a transactional group needs at least two, "+
			"and a single sink already gets atomicity from its own transaction", len(members))
	}

	seen := map[string]bool{}
	var notParticipants []string
	for _, m := range members {
		if m.ID == "" {
			return nil, errors.New("txgroup: every member needs a stable ID")
		}
		if seen[m.ID] {
			return nil, fmt.Errorf("txgroup: duplicate member ID %q; recovery could not tell them apart", m.ID)
		}
		seen[m.ID] = true

		if m.Sink == nil {
			return nil, fmt.Errorf("txgroup: member %q has no sink", m.ID)
		}
		if _, ok := m.Sink.(hermod.TwoPhaseCommit); !ok {
			notParticipants = append(notParticipants, m.ID)
		}
	}
	if len(notParticipants) > 0 {
		return nil, fmt.Errorf("txgroup: %v cannot take part in a distributed transaction "+
			"(they do not implement two-phase commit). Remove them from the group or write them "+
			"as ordinary sinks: a group is only atomic if every member can prepare", notParticipants)
	}

	return &Sink{members: members, coordinator: coordinator, logger: logger}, nil
}

// Preflight verifies that every member can genuinely honour the transaction
// contract, and returns an error naming the ones that cannot.
//
// Implementing hermod.TwoPhaseCommit is not the same as being able to honour it
// right now — a PostgreSQL sink behind a pooler quietly commits when asked to
// prepare, which inside a group means the coordinator believes it can still
// roll back while the data is already durable. Members that expose
// hermod.TwoPhaseCommitPreflight get asked; the rest are taken at their word.
//
// Call this at start-up, before Recover. Failing here costs a clear error at
// boot; not failing here costs a silently half-applied batch later.
func (s *Sink) Preflight(ctx context.Context) error {
	var problems []string
	for _, m := range s.members {
		p, ok := m.Sink.(hermod.TwoPhaseCommitPreflight)
		if !ok {
			continue
		}
		if err := p.PreflightTwoPhaseCommit(ctx); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", m.ID, err))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("txgroup: %d member(s) cannot take part in a distributed transaction: %v",
			len(problems), problems)
	}
	return nil
}

// Participants exposes the members as coordinator participants. Used by Recover
// and Reap, which need to map log records back to live sinks.
func (s *Sink) Participants() map[string]hermod.TwoPhaseCommit {
	out := make(map[string]hermod.TwoPhaseCommit, len(s.members))
	for _, m := range s.members {
		if p, ok := m.Sink.(hermod.TwoPhaseCommit); ok {
			out[m.ID] = p
		}
	}
	return out
}

// Recover resolves transactions left in doubt by a previous run. Call once at
// start-up, before the pipeline writes anything.
func (s *Sink) Recover(ctx context.Context) error {
	return s.coordinator.Recover(ctx, s.Participants())
}

// Reap rolls back transactions that have been in doubt past the coordinator's
// limit. Call periodically: this is what stops a coordinator that died
// mid-round from leaving PostgreSQL locks held indefinitely.
func (s *Sink) Reap(ctx context.Context) (int, error) {
	return s.coordinator.Reap(ctx, s.Participants())
}

// Write sends one message to every member atomically.
func (s *Sink) Write(ctx context.Context, msg hermod.Message) error {
	if msg == nil {
		return errors.New("txgroup: nil message")
	}
	return s.WriteBatch(ctx, []hermod.Message{msg})
}

// WriteBatch sends a batch to every member atomically.
//
// The writes happen first and the vote second, because a member can only
// prepare changes it already holds. If any write fails, nothing is prepared.
func (s *Sink) WriteBatch(ctx context.Context, msgs []hermod.Message) error {
	filtered := make([]hermod.Message, 0, len(msgs))
	for _, m := range msgs {
		if m != nil {
			filtered = append(filtered, m)
		}
	}
	if len(filtered) == 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	participants := make([]twopc.Participant, 0, len(s.members))
	for _, m := range s.members {
		p, ok := m.Sink.(hermod.TwoPhaseCommit)
		if !ok {
			// New rejects these, so reaching here means the group was mutated
			// behind our back. Fail rather than silently write non-atomically.
			return fmt.Errorf("txgroup: member %q no longer implements two-phase commit", m.ID)
		}
		participants = append(participants, twopc.Participant{ID: m.ID, Sink: p})
	}

	return s.coordinator.Run(ctx, participants, func(ctx context.Context) error {
		for _, m := range s.members {
			if err := s.writeMember(ctx, m, filtered); err != nil {
				return fmt.Errorf("member %q: %w", m.ID, err)
			}
		}
		return nil
	})
}

// writeMember hands the batch to one member, preferring its batch path.
func (s *Sink) writeMember(ctx context.Context, m Member, msgs []hermod.Message) error {
	if bs, ok := m.Sink.(hermod.BatchSink); ok {
		return bs.WriteBatch(ctx, msgs)
	}
	for _, msg := range msgs {
		if err := m.Sink.Write(ctx, msg); err != nil {
			return err
		}
	}
	return nil
}

// Ping reports the group healthy only when every member is. A group is only as
// available as its least available member, since one that cannot prepare aborts
// the batch for all of them.
func (s *Sink) Ping(ctx context.Context) error {
	var unhealthy []string
	for _, m := range s.members {
		if err := m.Sink.Ping(ctx); err != nil {
			unhealthy = append(unhealthy, fmt.Sprintf("%s: %v", m.ID, err))
		}
	}
	if len(unhealthy) > 0 {
		return fmt.Errorf("txgroup: %d of %d members unhealthy: %v", len(unhealthy), len(s.members), unhealthy)
	}
	return nil
}

// Close closes every member, returning the first failure but always attempting
// all of them: a member left open holds a connection for the life of the
// process.
func (s *Sink) Close() error {
	var firstErr error
	for _, m := range s.members {
		if err := m.Sink.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("txgroup: closing member %q: %w", m.ID, err)
		}
	}
	return firstErr
}

// InDoubt reports how many transactions this group has prepared and not
// resolved. Exposed so the reaper can publish it; see telemetry.TxGroupInDoubt
// for why that number matters more than most.
func (s *Sink) InDoubt(ctx context.Context) (int, error) {
	return s.coordinator.InDoubt(ctx)
}
