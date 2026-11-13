// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package selfheal

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gsoultan/hermod/internal/storage"
)

// Fix kinds a person can ask for from a recommendation. Each is a fixed,
// bounded change: the caller names the kind, never the values.
const (
	KindTraceSampling = "set_trace_sampling"
	KindLookupCache   = "enable_lookup_cache"
)

// What the fixes set.
const (
	// fixTraceSampleRate records one message in a hundred.
	fixTraceSampleRate = 0.01
	// fixLookupTTL is api_lookup's own default, the shorter of the two
	// lookups' defaults.
	fixLookupTTL = "5m"
)

// Fix is a request to propose one of the known fixes.
type Fix struct {
	WorkflowID string
	Kind       string
	NodeID     string
	Reason     string
}

// ProposeFix stores a known fix as a pending proposal and returns it. Asked
// again while the same fix is pending, it returns that proposal. A fix that
// is unknown, or would change nothing, is ErrNothingToFix.
func (s *Service) ProposeFix(ctx context.Context, fix Fix) (Proposal, error) {
	wf, err := s.store.GetWorkflow(ctx, fix.WorkflowID)
	if err != nil {
		return Proposal{}, err
	}
	title, patch := fixPatch(wf, fix)
	if len(patch) == 0 {
		return Proposal{}, ErrNothingToFix
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	ps, err := load(ctx, s.store, wf.ID)
	if err != nil {
		return Proposal{}, err
	}
	now := s.now().UTC()
	if i := slices.IndexFunc(ps, func(p Proposal) bool {
		return p.Status == StatusPending && p.Kind == fix.Kind && p.NodeID == fix.NodeID
	}); i >= 0 {
		ps[i].LastSeenAt = now
		return ps[i], save(ctx, s.store, wf.ID, ps)
	}
	p := Proposal{
		ID: uuid.NewString(), WorkflowID: wf.ID, Kind: fix.Kind, NodeID: fix.NodeID,
		Title: title, Reason: fix.Reason, Patch: patch,
		Status: StatusPending, Occurrences: 1, CreatedAt: now, LastSeenAt: now,
	}
	return p, save(ctx, s.store, wf.ID, append(ps, p))
}

// fixPatch is the change a fix makes to wf as it is now; none when it would
// change nothing.
func fixPatch(wf storage.Workflow, fix Fix) (string, []Change) {
	switch fix.Kind {
	case KindTraceSampling:
		if wf.TraceSampleRate > 0 {
			return "", nil
		}
		return "Record 1% of runs step by step", []Change{replace("/trace_sample_rate", wf.TraceSampleRate, fixTraceSampleRate)}
	case KindLookupCache:
		i := slices.IndexFunc(wf.Nodes, func(n storage.WorkflowNode) bool { return n.ID == fix.NodeID })
		if i < 0 {
			return "", nil
		}
		n := wf.Nodes[i]
		tt, _ := n.Config["transType"].(string)
		ttl, _ := n.Config["ttl"].(string)
		if n.Type != "transformation" || (tt != "db_lookup" && tt != "api_lookup") || !ttlIsZero(ttl) {
			return "", nil
		}
		return "Cache this lookup's results for 5 minutes", []Change{replace(nodeTTLPath(n.ID), ttl, fixLookupTTL)}
	}
	return "", nil
}

// ttlIsZero reports an explicit ttl of zero: the lookup's "do not cache".
func ttlIsZero(ttl string) bool {
	ttl = strings.TrimSpace(ttl)
	if ttl == "" {
		return false
	}
	if ttl == "0" {
		return true
	}
	d, err := time.ParseDuration(ttl)
	return err == nil && d == 0
}

// ApproveWithBaseline approves like Approve and keeps how the flow was
// running beforehand with the proposal.
func (s *Service) ApproveWithBaseline(ctx context.Context, workflowID, id, username string, baseline *Readings) (Proposal, error) {
	return s.approve(ctx, workflowID, id, username, baseline)
}
