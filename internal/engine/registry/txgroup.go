// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package registry

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/factory"
	"github.com/gsoultan/hermod/pkg/comm/sink/txgroup"
	"github.com/gsoultan/hermod/pkg/engine/twopc"
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
// Read the operational hazard note in docs/transactional-sink-groups.md before using this: a prepared
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

	group, err := r.startTxGroup(ctx, fmt.Sprintf("txgroup sink %q", cfg.ID), cfg.ID, members, coordinator)
	if err != nil {
		// Not the typed nil: a nil *txgroup.Sink in a hermod.Sink is not nil.
		return nil, err
	}
	return group, nil
}

// startTxGroup assembles a group from its members and coordinator and makes it
// safe to write to. label names the group in errors: a stand-alone group by its
// sink ID, a workflow transaction by its name. id is the group's sink ID, which
// scopes the record of its runs as it scopes its log. The members are closed
// on failure.
func (r *Registry) startTxGroup(ctx context.Context, label, id string, members []txgroup.Member, coordinator *twopc.Coordinator) (*txgroup.Sink, error) {
	group, err := txgroup.New(members, coordinator, r.logger)
	if err != nil {
		closeMembers(members)
		return nil, fmt.Errorf("%s: %w", label, err)
	}

	// Preflight before anything is written. A member that cannot genuinely
	// prepare — behind a pooler, or on a server with max_prepared_transactions
	// at its default of 0 — must stop the workflow starting rather than fail
	// mid-batch, or worse, commit at prepare time and diverge silently.
	if err := group.Preflight(ctx); err != nil {
		_ = group.Close()
		return nil, fmt.Errorf("%s: %w", label, err)
	}

	// The runs carry on from what the last start recorded, so the run view
	// counts across restarts and reads the same on every worker.
	group.KeepRunsIn(ctx, r.stateStore, txGroupRunsKey(id))

	// Resolve anything the previous run left in doubt, before this one writes.
	// Skipping it would let a new transaction stack on top of an unresolved one.
	if err := group.Recover(ctx); err != nil {
		_ = group.Close()
		return nil, fmt.Errorf("%s: unresolved transactions from a previous run: %w", label, err)
	}

	// Recover only runs at start-up, and the failure that hurts is a coordinator
	// that prepares, dies and never returns — for which start-up never happens
	// again. The reaper is what bounds that, so it runs for the life of the
	// group. It stops when the registry's context is cancelled.
	group.StartReaper(ctx, 0)

	return group, nil
}

// checkTxGroup answers "Test connection" for a group: everything that would
// stop it starting, checked without starting it.
//
// It does not build the group. Building one recovers its transaction log and
// starts its reaper, and a check run beside a live workflow must not resolve
// that workflow's in-flight transactions. So it checks what building would
// check — member count, the log, the age, each member's type and its
// preflight — and nothing else. It needs no ID, which an unsaved group lacks.
func (r *Registry) checkTxGroup(ctx context.Context, cfg factory.SinkConfig) error {
	memberIDs := splitMemberIDs(cfg.Config["members"])
	if len(memberIDs) < 2 {
		return fmt.Errorf("choose at least two member sinks (got %d); "+
			"a single sink already gets atomicity from its own transaction", len(memberIDs))
	}
	if problem := r.TxGroupLogProblem(); problem != "" {
		return errors.New(problem)
	}
	if raw := strings.TrimSpace(cfg.Config["max_prepared_age"]); raw != "" {
		if _, err := time.ParseDuration(raw); err != nil {
			return fmt.Errorf("max_prepared_age %q is not a duration such as 15m or 1h: %w", raw, err)
		}
	}

	var problems []string
	for _, id := range memberIDs {
		name := id
		if stored, err := r.GetSinkConfig(ctx, id); err == nil && stored.Name != "" {
			name = stored.Name
		}
		if err := r.checkTxGroupMember(ctx, id); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", name, err))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("%d of %d members cannot take part: %s",
			len(problems), len(memberIDs), strings.Join(problems, "; "))
	}
	return nil
}

// checkTxGroupMember builds one member as the group would, asks it whether it
// can really prepare, and closes it.
func (r *Registry) checkTxGroupMember(ctx context.Context, id string) error {
	snk, err := r.resolveAndCreateTxGroupMember(ctx, id)
	if err != nil {
		return err
	}
	defer func() { _ = snk.Close() }()
	if p, ok := snk.(hermod.TwoPhaseCommitPreflight); ok {
		return p.PreflightTwoPhaseCommit(ctx)
	}
	return snk.Ping(ctx)
}

// newTxCoordinator builds the coordinator backing a group.
//
// The transaction log has to outlive the process, or a crash leaves
// participants in doubt with nothing able to resolve them. So a missing state
// store is refused rather than quietly replaced with an in-memory one: a group
// that cannot recover is worse than a group that will not start.
func (r *Registry) newTxCoordinator(cfg factory.SinkConfig) (*twopc.Coordinator, error) {
	// Read without taking r.mu: StartWorkflow holds it while it builds sinks.
	if problem := txGroupLogProblem(r.stateStore); problem != "" {
		return nil, fmt.Errorf("txgroup sink %q: %s", cfg.ID, problem)
	}
	if cfg.ID == "" {
		return nil, errors.New("txgroup sink: an ID is required to scope its transaction log")
	}

	opts := twopc.Options{
		Store:      twopc.NewIndexedStore(r.stateStore, txGroupIndexKey(cfg.ID)),
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

// txGroupIndexKey is where the log of the group with this sink ID keeps its
// index; the coordinator's WorkflowID is the sink ID itself.
func txGroupIndexKey(id string) string { return "twopc-index/" + id }

// txGroupRunsKey is where the group with this sink ID records its runs.
func txGroupRunsKey(id string) string { return "twopc-runs/" + id }

// TxGroupLogProblem says why a transactional group could not keep a durable
// transaction log right now, or returns "" when it can. createTxGroupSink
// refuses to build a group for the same reason, and the editor shows it before
// the workflow is ever run, so the two cannot disagree.
//
// The log has to outlive the process: a crash between the vote and the
// decision leaves members holding prepared transactions that only the log can
// resolve. A SQLite file does that for a single Hermod server. With several
// workers the log must be shared, so Redis or Etcd.
func (r *Registry) TxGroupLogProblem() string {
	// Settings can replace the store while a request reads it. Only callers
	// outside the registry come here; the registry's own path already holds mu.
	r.mu.RLock()
	ss := r.stateStore
	r.mu.RUnlock()
	return txGroupLogProblem(ss)
}

func txGroupLogProblem(ss hermod.StateStore) string {
	if ss == nil {
		return "a state store is required so the coordinator's transaction log survives a restart, " +
			"and none is configured. Set one in Settings → Platform → Global State Store: SQLite for a " +
			"single Hermod server, Redis or Etcd when several workers share the work"
	}
	if d, ok := ss.(interface{ Durable() bool }); ok && !d.Durable() {
		return "the state store keeps its data in memory, so the coordinator's transaction log would " +
			"not survive a restart. Choose SQLite, Redis or Etcd in Settings → Platform → Global State Store"
	}
	return ""
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
