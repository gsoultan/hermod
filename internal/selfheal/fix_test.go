// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package selfheal_test

import (
	"context"
	"errors"
	"testing"

	"github.com/gsoultan/hermod/internal/selfheal"
	"github.com/gsoultan/hermod/internal/storage"
)

func lookupFlow() storage.Workflow {
	return storage.Workflow{ID: "wf", Name: "orders", Nodes: []storage.WorkflowNode{
		{ID: "src", Type: "source"},
		{ID: "lookup", Type: "transformation", Config: map[string]any{"transType": "api_lookup", "ttl": "0", "url": "http://x"}},
	}}
}

func TestProposeFixTurnsOnTraceSampling(t *testing.T) {
	st := newStore(lookupFlow())
	svc := selfheal.NewService(st, &applier{store: st})
	ctx := context.Background()

	p, err := svc.ProposeFix(ctx, selfheal.Fix{WorkflowID: "wf", Kind: selfheal.KindTraceSampling, Reason: "Runs are not being recorded"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Status != selfheal.StatusPending || len(p.Patch) != 1 || p.Patch[0].Path != "/trace_sample_rate" {
		t.Fatalf("proposal = %+v", p)
	}
	if string(p.Patch[0].Before) != "0" || string(p.Patch[0].After) != "0.01" {
		t.Fatalf("patch = %s -> %s", p.Patch[0].Before, p.Patch[0].After)
	}
	if wf, _ := st.GetWorkflow(ctx, "wf"); wf.TraceSampleRate != 0 {
		t.Fatal("proposing changed the workflow")
	}

	if _, err := svc.Approve(ctx, "wf", p.ID, "ada"); err != nil {
		t.Fatal(err)
	}
	if wf, _ := st.GetWorkflow(ctx, "wf"); wf.TraceSampleRate != 0.01 {
		t.Fatalf("trace sample rate = %v after approval", wf.TraceSampleRate)
	}
}

func TestProposeFixTurnsOnALookupCacheForOneNodeOnly(t *testing.T) {
	st := newStore(lookupFlow())
	ap := &applier{store: st}
	svc := selfheal.NewService(st, ap)
	ctx := context.Background()

	p, err := svc.ProposeFix(ctx, selfheal.Fix{WorkflowID: "wf", Kind: selfheal.KindLookupCache, NodeID: "lookup", Reason: "cache off"})
	if err != nil {
		t.Fatal(err)
	}
	if p.NodeID != "lookup" || p.Patch[0].Path != "/nodes/lookup/config/ttl" || string(p.Patch[0].After) != `"5m"` {
		t.Fatalf("proposal = %+v", p)
	}
	before, _ := st.GetWorkflow(ctx, "wf")
	if _, err := svc.Approve(ctx, "wf", p.ID, "ada"); err != nil {
		t.Fatal(err)
	}
	wf, _ := st.GetWorkflow(ctx, "wf")
	if wf.Nodes[1].Config["ttl"] != "5m" || wf.Nodes[1].Config["url"] != "http://x" {
		t.Fatalf("node config = %+v", wf.Nodes[1].Config)
	}
	// The stored workflow the patch was computed from must not be written to:
	// node configs are maps, shared by every copy of the workflow value.
	if before.Nodes[1].Config["ttl"] != "0" {
		t.Fatal("applying the patch wrote through to the workflow it was read from")
	}
}

func TestProposeFixRefusesWhatWouldChangeNothing(t *testing.T) {
	wf := lookupFlow()
	wf.TraceSampleRate = 0.05
	wf.Nodes[1].Config["ttl"] = "10m"
	st := newStore(wf)
	svc := selfheal.NewService(st, nil)
	for _, fix := range []selfheal.Fix{
		{WorkflowID: "wf", Kind: selfheal.KindTraceSampling},
		{WorkflowID: "wf", Kind: selfheal.KindLookupCache, NodeID: "lookup"},
		{WorkflowID: "wf", Kind: selfheal.KindLookupCache, NodeID: "src"},
		{WorkflowID: "wf", Kind: selfheal.KindLookupCache, NodeID: "nope"},
		{WorkflowID: "wf", Kind: "rewrite_everything"},
	} {
		if _, err := svc.ProposeFix(context.Background(), fix); !errors.Is(err, selfheal.ErrNothingToFix) {
			t.Errorf("%+v: err = %v, want ErrNothingToFix", fix, err)
		}
	}
}

func TestProposeFixDoesNotRepeatAPendingProposal(t *testing.T) {
	st := newStore(lookupFlow())
	svc := selfheal.NewService(st, nil)
	ctx := context.Background()
	fix := selfheal.Fix{WorkflowID: "wf", Kind: selfheal.KindLookupCache, NodeID: "lookup"}
	first, err := svc.ProposeFix(ctx, fix)
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.ProposeFix(ctx, fix)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID {
		t.Fatal("the same fix was proposed twice")
	}
	if ps, _ := svc.List(ctx, "wf"); len(ps) != 1 {
		t.Fatalf("%d proposals, want 1", len(ps))
	}
}

func TestApproveRefusesALookupPatchWhoseNodeChanged(t *testing.T) {
	st := newStore(lookupFlow())
	svc := selfheal.NewService(st, &applier{store: st})
	ctx := context.Background()
	p, _ := svc.ProposeFix(ctx, selfheal.Fix{WorkflowID: "wf", Kind: selfheal.KindLookupCache, NodeID: "lookup"})

	wf := lookupFlow()
	wf.Nodes[1].Config["ttl"] = "30s"
	st.workflows["wf"] = wf

	if _, err := svc.Approve(ctx, "wf", p.ID, "ada"); !errors.Is(err, selfheal.ErrStale) {
		t.Fatalf("err = %v, want ErrStale", err)
	}
}

func TestApproveKeepsTheReadingsFromBeforeTheFix(t *testing.T) {
	st := newStore(lookupFlow())
	svc := selfheal.NewService(st, &applier{store: st})
	ctx := context.Background()
	p, _ := svc.ProposeFix(ctx, selfheal.Fix{WorkflowID: "wf", Kind: selfheal.KindLookupCache, NodeID: "lookup"})

	base := &selfheal.Readings{Throughput: 38.9, StepTimeMs: 40, ErrorRate: 0.01, Messages: 7000, WindowSeconds: 180}
	got, err := svc.ApproveWithBaseline(ctx, "wf", p.ID, "ada", base)
	if err != nil {
		t.Fatal(err)
	}
	if got.Baseline == nil || got.Baseline.StepTimeMs != 40 {
		t.Fatalf("baseline = %+v", got.Baseline)
	}
	ps, _ := svc.List(ctx, "wf")
	if ps[0].Baseline == nil || ps[0].Baseline.Throughput != 38.9 {
		t.Fatal("the baseline was not stored with the proposal")
	}
}
