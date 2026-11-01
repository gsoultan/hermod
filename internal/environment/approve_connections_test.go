// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package environment

import (
	"context"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
)

// A promotion can wait hours for approval, and production's connections can
// change in that time. Receiving it checked they existed; approving it has to
// check again, or the workflow goes live pointing at a sink that is gone.
func TestApproveChecksTheConnectionsAreStillThere(t *testing.T) {
	cases := []struct {
		name   string
		change func(p *pair)
		// wantMissing is the connection the failure names; empty means the
		// approval deploys.
		wantMissing string
	}{
		{name: "unchanged", change: func(*pair) {}},
		{name: "sink deleted", change: func(p *pair) { delete(p.prdStore.sinks, "p-wh") }, wantMissing: "p-wh"},
		{name: "source deleted", change: func(p *pair) { delete(p.prdStore.sources, "p-orders") }, wantMissing: "p-orders"},
		{name: "lookup source deleted", change: func(p *pair) { delete(p.prdStore.sources, "p-crm") }, wantMissing: "p-crm"},
		{name: "sink moved to another vhost", change: func(p *pair) {
			s := p.prdStore.sinks["p-wh"]
			s.VHost = "payroll"
			p.prdStore.sinks["p-wh"] = s
		}, wantMissing: "p-wh"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			p, l := promotable(t)
			sent, err := p.stg.Promote(ctx, l.ID, "wf-orders", "gembit", PromoteOptions{Start: true})
			if err != nil {
				t.Fatal(err)
			}
			tc.change(p)

			got, err := p.prd.Approve(ctx, sent.ID, "rina")
			if tc.wantMissing == "" {
				if err != nil || got.Status != StatusDeployed {
					t.Fatalf("Approve = %+v, %v; want it deployed", got, err)
				}
				if wf := p.prdStore.workflows["wf-orders"]; !wf.Active {
					t.Fatalf("deployed workflow = %+v, want it running", wf)
				}
				return
			}
			if err == nil {
				t.Fatalf("Approve deployed a workflow whose connection %s is gone: %+v", tc.wantMissing, got)
			}
			if got.Status != StatusFailed || !strings.Contains(got.Reason, tc.wantMissing) {
				t.Fatalf("promotion = %+v, want failed naming %s", got, tc.wantMissing)
			}
			if _, ok := p.prdStore.workflows["wf-orders"]; ok {
				t.Fatal("the workflow was stored in production anyway")
			}
		})
	}
}

// An update to a workflow production already runs is held to the same
// check, and leaves production's running version alone when it fails.
func TestApproveKeepsTheRunningVersionWhenAConnectionIsGone(t *testing.T) {
	ctx := context.Background()
	p, l := promotable(t)
	first, err := p.stg.Promote(ctx, l.ID, "wf-orders", "gembit", PromoteOptions{Start: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.prd.Approve(ctx, first.ID, "rina"); err != nil {
		t.Fatal(err)
	}

	// Staging's v2 adds a second sink; production has it when the release
	// is sent and deletes it before anyone approves.
	p.stgStore.sinks["k-audit"] = storage.Sink{ID: "k-audit", Name: "audit", Type: "snowflake", VHost: "default"}
	p.prdStore.sinks["p-audit"] = storage.Sink{ID: "p-audit", Name: "audit", Type: "snowflake", VHost: "default"}
	v2 := ordersWorkflow()
	v2.Nodes = append(v2.Nodes, storage.WorkflowNode{ID: "n4", Type: "sink", RefID: "k-audit"})
	v2.Edges = append(v2.Edges, storage.WorkflowEdge{ID: "e3", SourceID: "n2", TargetID: "n4"})
	p.stgStore.saveVersion(v2, "gembit", "add audit")
	second, err := p.stg.Promote(ctx, l.ID, "wf-orders", "gembit", PromoteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	delete(p.prdStore.sinks, "p-audit")

	got, err := p.prd.Approve(ctx, second.ID, "rina")
	if err == nil || got.Status != StatusFailed {
		t.Fatalf("Approve = %+v, %v; want it to fail", got, err)
	}
	wf := p.prdStore.workflows["wf-orders"]
	if len(wf.Nodes) != 3 || !wf.Active {
		t.Fatalf("production's workflow = %d nodes, active %v; want v1 still running", len(wf.Nodes), wf.Active)
	}
}

// In a release, the member whose connection went away fails the whole
// release: the members deployed before it are undone, so none of it stays
// live.
func TestAReleaseIsNotDeployedWhenOneConnectionIsGone(t *testing.T) {
	ctx := context.Background()
	f := newFleet(t)
	f.stgStore.sources["s-absence"] = storage.Source{ID: "s-absence", Name: "absence-db", Type: "postgres", VHost: "HR"}
	f.hrStore.sources["p-absence"] = storage.Source{ID: "p-absence", Name: "absence-db", Type: "postgres", VHost: "hr"}
	f.stgStore.saveVersion(hrWorkflow("wf-leave", "leave requests", "s-absence"), "gembit", "own source")
	sent, err := f.stg.SendRelease(ctx, items("wf-onb", "wf-leave"), "gembit", PromoteOptions{Start: true})
	if err != nil {
		t.Fatal(err)
	}
	delete(f.hrStore.sources, "p-absence")

	if _, err := f.hr.Approve(ctx, sent[0].Promotions[0].ID, "rina"); err == nil {
		t.Fatal("a release with a deleted connection was deployed")
	}
	status := map[string]Promotion{}
	ps, _ := f.hr.Promotions(ctx)
	for _, p := range ps {
		status[p.WorkflowID] = p
	}
	if got := status["wf-leave"]; got.Status != StatusFailed || !strings.Contains(got.Reason, "p-absence") {
		t.Fatalf("wf-leave = %+v, want failed naming p-absence", got)
	}
	if got := status["wf-onb"]; got.Status != StatusRolledBack {
		t.Fatalf("wf-onb = %s, want it undone with the rest of the release", got.Status)
	}
	if f.hrStore.workflows["wf-onb"].Active {
		t.Fatal("wf-onb is running after its release failed")
	}
	if _, ok := f.hrStore.workflows["wf-leave"]; ok {
		t.Fatal("wf-leave was stored in production anyway")
	}
}
