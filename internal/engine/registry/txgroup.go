package registry

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gsoultan/Hermod"
	"github.com/gsoultan/Hermod/internal/factory"
	"github.com/gsoultan/Hermod/pkg/comm/sink/txgroup"
	"github.com/gsoultan/Hermod/pkg/engine/twopc"
)

// createTxGroupSink builds a transactional sink group: several sinks that
// commit atomically, either all applying a batch or none.
//
// It is a sink type rather than a property of the sinks themselves so that the
// group is a single node in the DAG. The engine then drives it through one
// ordinary writer, which is what lets two-phase commit happen at all — every
// sink otherwise gets its own writer goroutine with its own batching loop, and
// independent batches cannot share a transaction boundary.
//
// Configuration:
//
//	type:    "txgroup"
//	members: comma-separated sink IDs (at least two)
//	max_prepared_age: optional, e.g. "15m" — how long a transaction may sit in
//	                  doubt before the reaper rolls it back
//
// Read the operational hazard note in README.md before using this: a prepared
// PostgreSQL transaction holds locks and blocks VACUUM until it is resolved.
func (r *Registry) createTxGroupSink(ctx context.Context, cfg factory.SinkConfig) (hermod.Sink, error) {
	memberIDs := splitMemberIDs(cfg.Config["members"])
	if len(memberIDs) < 2 {
		return nil, fmt.Errorf("txgroup sink %q: needs at least two members in 'members' (got %d); "+
			"a single sink already gets atomicity from its own transaction", cfg.ID, len(memberIDs))
	}

	coordinator, err := r.newTxCoordinator(cfg)
	if err != nil {
		return nil, err
	}

	members := make([]txgroup.Member, 0, len(memberIDs))
	for _, id := range memberIDs {
		snk, err := r.resolveAndCreateTxGroupMember(ctx, id)
		if err != nil {
			closeMembers(members)
			return nil, fmt.Errorf("txgroup sink %q: cannot create member %q: %w", cfg.ID, id, err)
		}
		members = append(members, txgroup.Member{ID: id, Sink: snk})
	}

	group, err := txgroup.New(members, coordinator, r.logger)
	if err != nil {
		closeMembers(members)
		return nil, fmt.Errorf("txgroup sink %q: %w", cfg.ID, err)
	}

	// Preflight before anything is written. A member that cannot genuinely
	// prepare — behind a pooler, or on a server with max_prepared_transactions
	// at its default of 0 — must stop the workflow starting rather than fail
	// mid-batch, or worse, commit at prepare time and diverge silently.
	if err := group.Preflight(ctx); err != nil {
		_ = group.Close()
		return nil, fmt.Errorf("txgroup sink %q: %w", cfg.ID, err)
	}

	// Resolve anything the previous run left in doubt, before this one writes.
	// Skipping it would let a new transaction stack on top of an unresolved one.
	if err := group.Recover(ctx); err != nil {
		_ = group.Close()
		return nil, fmt.Errorf("txgroup sink %q: unresolved transactions from a previous run: %w", cfg.ID, err)
	}

	// Recover only runs at start-up, and the failure that hurts is a coordinator
	// that prepares, dies and never returns — for which start-up never happens
	// again. The reaper is what bounds that, so it runs for the life of the
	// group. It stops when the registry's context is cancelled.
	group.StartReaper(ctx, 0)

	return group, nil
}

// newTxCoordinator builds the coordinator backing a group.
//
// The transaction log has to outlive the process, or a crash leaves
// participants in doubt with nothing able to resolve them. So a missing state
// store is refused rather than quietly replaced with an in-memory one: a group
// that cannot recover is worse than a group that will not start.
func (r *Registry) newTxCoordinator(cfg factory.SinkConfig) (*twopc.Coordinator, error) {
	if r.stateStore == nil {
		return nil, fmt.Errorf("txgroup sink %q: a state store is required so the coordinator's "+
			"transaction log survives a restart; configure distributed state (Redis or Etcd) "+
			"before using a transactional group", cfg.ID)
	}
	if cfg.ID == "" {
		return nil, errors.New("txgroup sink: an ID is required to scope its transaction log")
	}

	opts := twopc.Options{
		Store:      twopc.NewIndexedStore(r.stateStore, "twopc-index/"+cfg.ID),
		WorkflowID: cfg.ID,
		Logger:     r.logger,
	}
	if raw := strings.TrimSpace(cfg.Config["max_prepared_age"]); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return nil, fmt.Errorf("txgroup sink %q: max_prepared_age %q is not a duration: %w", cfg.ID, raw, err)
		}
		opts.MaxPreparedAge = d
	}

	c, err := twopc.New(opts)
	if err != nil {
		return nil, fmt.Errorf("txgroup sink %q: %w", cfg.ID, err)
	}
	return c, nil
}

// splitMemberIDs parses the comma-separated member list, discarding blanks so a
// trailing comma is not read as an anonymous member.
func splitMemberIDs(raw string) []string {
	var out []string
	for part := range strings.SplitSeq(raw, ",") {
		if id := strings.TrimSpace(part); id != "" {
			out = append(out, id)
		}
	}
	return out
}

// closeMembers releases sinks built before construction failed, so a rejected
// group does not leak the connections it opened.
func closeMembers(members []txgroup.Member) {
	for _, m := range members {
		if m.Sink != nil {
			_ = m.Sink.Close()
		}
	}
}
