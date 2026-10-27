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
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
)

// ordersWorkflow is a staging workflow reading orders-db, enriching from
// crm through a lookup, and writing to warehouse.
func ordersWorkflow() storage.Workflow {
	return storage.Workflow{
		ID: "wf-orders", Name: "orders-cdc", VHost: "default", Active: true, Status: "Running",
		WorkerID: "stg-worker", TotalProcessed: 1200,
		Nodes: []storage.WorkflowNode{
			{ID: "n1", Type: "source", RefID: "s-orders", Config: map[string]any{"label": "Orders"}},
			{ID: "n2", Type: "transformer", Config: map[string]any{"label": "Lookup", "sourceId": "s-crm",
				"api_key": "{{secret.CRM_KEY}}", "region": "{{ env.REGION }}"}},
			{ID: "n3", Type: "sink", RefID: "k-wh", Config: map[string]any{"label": "Warehouse", "batch_size": 500}},
		},
		Edges: []storage.WorkflowEdge{{ID: "e1", SourceID: "n1", TargetID: "n2"}, {ID: "e2", SourceID: "n2", TargetID: "n3"}},
	}
}

// promotable is a linked pair whose staging has ordersWorkflow saved as
// version 1 and whose production has every connection it needs.
func promotable(t *testing.T) (*pair, Link) {
	t.Helper()
	p := newPair(t)
	p.stgStore.sources["s-orders"] = storage.Source{ID: "s-orders", Name: "orders-db", Type: "postgres", VHost: "default"}
	p.stgStore.sources["s-crm"] = storage.Source{ID: "s-crm", Name: "crm-stg", Type: "mysql", VHost: "default"}
	p.stgStore.sinks["k-wh"] = storage.Sink{ID: "k-wh", Name: "warehouse", Type: "snowflake", VHost: "default"}
	p.prdStore.sources["p-orders"] = storage.Source{ID: "p-orders", Name: "orders-db", Type: "postgres", VHost: "default"}
	p.prdStore.sources["p-crm"] = storage.Source{ID: "p-crm", Name: "crm", Type: "mysql", VHost: "default"}
	p.prdStore.sinks["p-wh"] = storage.Sink{ID: "p-wh", Name: "warehouse", Type: "snowflake", VHost: "default"}
	p.secrets.secrets["default/CRM_KEY"] = true
	p.secrets.envs["REGION"] = true
	p.stgStore.saveVersion(ordersWorkflow(), "gembit", "first")
	l := p.link(t)
	// crm-stg only suggests crm; a person confirms it.
	l, err := p.stg.SetMapping(context.Background(), l.ID, map[string]string{"s-crm": "p-crm"})
	if err != nil {
		t.Fatal(err)
	}
	return p, l
}

func checkNamed(checks []Check, prefix string) (Check, bool) {
	i := slices.IndexFunc(checks, func(c Check) bool { return strings.HasPrefix(c.Name, prefix) })
	if i < 0 {
		return Check{}, false
	}
	return checks[i], true
}

func TestPreviewOfANewWorkflow(t *testing.T) {
	p, l := promotable(t)
	pv, err := p.stg.Preview(context.Background(), l.ID, "wf-orders", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !pv.Ready || pv.FromVersion != 1 || pv.CurrentVersion != 0 {
		t.Fatalf("preview = %+v", pv)
	}
	for _, name := range []string{"Workflow is valid", "Connections matched", "Secrets exist", "Environment variables exist"} {
		if c, ok := checkNamed(pv.Checks, name); !ok || !c.OK {
			t.Errorf("check %q = %+v (found %v)", name, c, ok)
		}
	}
	if len(pv.Changes) != 1 || pv.Changes[0].Kind != "added" {
		t.Fatalf("changes = %+v", pv.Changes)
	}
}

func TestPromoteSendsTheWorkflowWithProductionConnections(t *testing.T) {
	ctx := context.Background()
	p, l := promotable(t)

	sent, err := p.stg.Promote(ctx, l.ID, "wf-orders", "gembit", PromoteOptions{Note: "first go", Start: true, AutoRollback: true})
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}
	if sent.Status != StatusPending || sent.FromEnv != "Production" || sent.PeerID != l.ID {
		t.Fatalf("sent = %+v", sent)
	}
	log, _ := p.stg.Sent(ctx)
	if len(log) != 1 || log[0].ID != sent.ID {
		t.Fatalf("sent log = %+v", log)
	}

	got, _ := p.prd.Promotions(ctx)
	if len(got) != 1 || got[0].FromEnv != "Staging" || got[0].Sender != "gembit" || got[0].Note != "first go" || got[0].FromVersion != 1 {
		t.Fatalf("production promotions = %+v", got)
	}
	wf, err := loadPromotionWorkflow(ctx, p.prdStore, got[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	refs := map[string]string{}
	for _, n := range wf.Nodes {
		refs[n.ID] = n.RefID
		if id, ok := n.Config["sourceId"].(string); ok {
			refs[n.ID] = id
		}
	}
	if refs["n1"] != "p-orders" || refs["n2"] != "p-crm" || refs["n3"] != "p-wh" {
		t.Fatalf("references were not rewritten to production's: %v", refs)
	}
	if wf.WorkerID != "" || wf.Status != "" || wf.TotalProcessed != 0 {
		t.Fatalf("staging's runtime columns travelled: %+v", wf)
	}
	// Staging's own workflow is untouched.
	if src, _ := p.stgStore.GetWorkflow(ctx, "wf-orders"); src.Nodes[0].RefID != "s-orders" || src.Nodes[1].Config["sourceId"] != "s-crm" {
		t.Fatalf("staging's workflow was changed: %+v", src.Nodes)
	}
	if _, ok := p.prdStore.workflows["wf-orders"]; ok {
		t.Fatal("the workflow was deployed before anyone approved it")
	}
}

func TestApproveDeploysAndTheSenderCannotApprove(t *testing.T) {
	ctx := context.Background()
	p, l := promotable(t)
	sent, err := p.stg.Promote(ctx, l.ID, "wf-orders", "gembit", PromoteOptions{Start: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.prd.Approve(ctx, sent.ID, "Gembit"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("sender approving: err = %v, want ErrForbidden", err)
	}
	got, err := p.prd.Approve(ctx, sent.ID, "rina")
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if got.Status != StatusDeployed || got.AppliedVersion != 1 || got.PreviousVersion != 0 || got.DecidedBy != "rina" {
		t.Fatalf("promotion = %+v", got)
	}
	wf := p.prdStore.workflows["wf-orders"]
	if !wf.Active || wf.Nodes[0].RefID != "p-orders" {
		t.Fatalf("deployed workflow = %+v", wf)
	}
	if _, err := p.prd.Approve(ctx, sent.ID, "rina"); !errors.Is(err, ErrConflict) {
		t.Fatalf("approving twice: err = %v, want ErrConflict", err)
	}
}

func TestPromotingAgainUpdatesAndKeepsProductionsRun(t *testing.T) {
	ctx := context.Background()
	p, l := promotable(t)
	first, _ := p.stg.Promote(ctx, l.ID, "wf-orders", "gembit", PromoteOptions{Start: true})
	if _, err := p.prd.Approve(ctx, first.ID, "rina"); err != nil {
		t.Fatal(err)
	}
	running := p.prdStore.workflows["wf-orders"]
	running.WorkerID, running.Status, running.TotalProcessed, running.WorkspaceID = "prd-worker", "Running", 99, "ws-prod"
	p.prdStore.workflows["wf-orders"] = running

	next := ordersWorkflow()
	next.Nodes[2].Config["batch_size"] = 1000
	next.Nodes = append(next.Nodes, storage.WorkflowNode{ID: "n4", Type: "filter", Config: map[string]any{"label": "Skip test orders"}})
	p.stgStore.saveVersion(next, "gembit", "second")

	pv, err := p.stg.Preview(ctx, l.ID, "wf-orders", 0)
	if err != nil || pv.FromVersion != 2 || pv.CurrentVersion != 1 {
		t.Fatalf("preview = %+v, %v", pv, err)
	}
	var kinds []string
	for _, c := range pv.Changes {
		kinds = append(kinds, c.Kind+" "+c.What)
	}
	if !slices.Contains(kinds, "added filter node Skip test orders") || !slices.Contains(kinds, "changed sink node Warehouse: batch_size") {
		t.Fatalf("changes = %v", kinds)
	}

	second, err := p.stg.Promote(ctx, l.ID, "wf-orders", "gembit", PromoteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.prd.Approve(ctx, second.ID, "rina")
	if err != nil {
		t.Fatal(err)
	}
	if got.PreviousVersion != 1 || got.AppliedVersion != 2 || got.ProcessedAtDeploy != 99 {
		t.Fatalf("promotion = %+v", got)
	}
	wf := p.prdStore.workflows["wf-orders"]
	if wf.WorkerID != "prd-worker" || wf.WorkspaceID != "ws-prod" || wf.TotalProcessed != 99 || !wf.Active || len(wf.Nodes) != 4 {
		t.Fatalf("production's run was not kept: %+v", wf)
	}
}

func TestPromoteIsRefusedWhileAConnectionIsUnmatched(t *testing.T) {
	ctx := context.Background()
	p, l := promotable(t)
	if _, err := p.stg.SetMapping(ctx, l.ID, map[string]string{"s-crm": ""}); err != nil {
		t.Fatal(err)
	}
	pv, err := p.stg.Preview(ctx, l.ID, "wf-orders", 0)
	if err != nil {
		t.Fatal(err)
	}
	if c, _ := checkNamed(pv.Checks, "Connections matched"); c.OK || !strings.Contains(c.Detail, "crm-stg") || pv.Ready {
		t.Fatalf("check = %+v, ready %v", c, pv.Ready)
	}
	if _, err := p.stg.Promote(ctx, l.ID, "wf-orders", "gembit", PromoteOptions{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Promote: err = %v, want ErrInvalid", err)
	}
	if got, _ := p.prd.Promotions(ctx); len(got) != 0 {
		t.Fatalf("production received %+v", got)
	}
}

func TestPreviewWhenProductionIsDown(t *testing.T) {
	p, l := promotable(t)
	p.remote.down = true
	pv, err := p.stg.Preview(context.Background(), l.ID, "wf-orders", 0)
	if err != nil {
		t.Fatal(err)
	}
	if c, ok := checkNamed(pv.Checks, "Production is reachable"); !ok || c.OK || pv.Ready {
		t.Fatalf("checks = %+v", pv.Checks)
	}
}

func TestMissingSecretsWarnButDoNotBlock(t *testing.T) {
	p, l := promotable(t)
	delete(p.secrets.secrets, "default/CRM_KEY")
	pv, err := p.stg.Preview(context.Background(), l.ID, "wf-orders", 0)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := checkNamed(pv.Checks, "Secrets exist")
	if c.OK || c.Blocking || !strings.Contains(c.Detail, "CRM_KEY") || !pv.Ready {
		t.Fatalf("check = %+v, ready %v", c, pv.Ready)
	}
}

func TestReceiveRefusesAConnectionProductionDoesNotHave(t *testing.T) {
	ctx := context.Background()
	p := newPair(t)
	wf := ordersWorkflow()
	_, err := p.prd.Receive(ctx, Peer{ID: "x", Name: "Staging"}, Submission{Workflow: wf, Sender: "gembit"})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "s-orders") {
		t.Fatalf("err = %v", err)
	}
}

func TestSoakTimeIsEnforcedOnBothSides(t *testing.T) {
	ctx := context.Background()
	p, l := promotable(t)
	if _, err := p.prd.SetRules(ctx, Rules{Approval: ApproveAnyAdmin, MinSoakMinutes: 72 * 60}); err != nil {
		t.Fatal(err)
	}
	pv, err := p.stg.Preview(ctx, l.ID, "wf-orders", 0)
	if err != nil {
		t.Fatal(err)
	}
	if c, ok := checkNamed(pv.Checks, "Has run long enough"); !ok || c.OK || pv.Ready {
		t.Fatalf("checks = %+v", pv.Checks)
	}
	// A sender that skips its own check is still refused.
	wf := ordersWorkflow()
	wf.Nodes = nil
	_, err = p.prd.Receive(ctx, Peer{ID: "x"}, Submission{Workflow: wf, FromVersionAt: time.Now().Add(-time.Hour)})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Receive: err = %v, want ErrInvalid", err)
	}
}

func TestNoApprovalRuleDeploysAtOnce(t *testing.T) {
	ctx := context.Background()
	p, l := promotable(t)
	if _, err := p.prd.SetRules(ctx, Rules{Approval: ApproveNone}); err != nil {
		t.Fatal(err)
	}
	sent, err := p.stg.Promote(ctx, l.ID, "wf-orders", "gembit", PromoteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if sent.Status != StatusDeployed || sent.DecidedBy != "automatic" {
		t.Fatalf("sent = %+v", sent)
	}
	if wf, ok := p.prdStore.workflows["wf-orders"]; !ok || wf.Active {
		t.Fatalf("deployed = %+v (Start was off, so it must not run)", wf)
	}
}

func TestRejectAndRejectedCannotBeApproved(t *testing.T) {
	ctx := context.Background()
	p, l := promotable(t)
	sent, _ := p.stg.Promote(ctx, l.ID, "wf-orders", "gembit", PromoteOptions{})
	got, err := p.prd.Reject(ctx, sent.ID, "rina", "not this week")
	if err != nil || got.Status != StatusRejected || got.Reason != "not this week" {
		t.Fatalf("Reject = %+v, %v", got, err)
	}
	if _, err := p.prd.Approve(ctx, sent.ID, "rina"); !errors.Is(err, ErrConflict) {
		t.Fatalf("approving a rejected promotion: %v", err)
	}
	if _, err := p.prd.Approve(ctx, "nope", "rina"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown promotion: %v", err)
	}
}

// deployTwice deploys version 1 then version 2 of the orders workflow.
func deployTwice(t *testing.T) (*pair, Promotion, Promotion) {
	t.Helper()
	ctx := context.Background()
	p, l := promotable(t)
	first, _ := p.stg.Promote(ctx, l.ID, "wf-orders", "gembit", PromoteOptions{Start: true, AutoRollback: true})
	first, err := p.prd.Approve(ctx, first.ID, "rina")
	if err != nil {
		t.Fatal(err)
	}
	next := ordersWorkflow()
	next.Name = "orders-cdc v2"
	p.stgStore.saveVersion(next, "gembit", "second")
	second, _ := p.stg.Promote(ctx, l.ID, "wf-orders", "gembit", PromoteOptions{AutoRollback: true})
	second, err = p.prd.Approve(ctx, second.ID, "rina")
	if err != nil {
		t.Fatal(err)
	}
	return p, first, second
}

func TestRollbackRestoresThePreviousVersion(t *testing.T) {
	ctx := context.Background()
	p, first, second := deployTwice(t)
	if _, err := p.prd.Rollback(ctx, first.ID, "rina", ""); !errors.Is(err, ErrConflict) {
		t.Fatalf("rolling back an older promotion: err = %v, want ErrConflict", err)
	}
	got, err := p.prd.Rollback(ctx, second.ID, "rina", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusRolledBack || got.Reason != "Rolled back by rina" {
		t.Fatalf("promotion = %+v", got)
	}
	if wf := p.prdStore.workflows["wf-orders"]; wf.Name != "orders-cdc" {
		t.Fatalf("workflow after rollback = %q, want the first version back", wf.Name)
	}
	if _, err := p.prd.Rollback(ctx, second.ID, "rina", ""); !errors.Is(err, ErrConflict) {
		t.Fatalf("rolling back twice: %v", err)
	}
	// With the newer one rolled back, the first is the newest deployed and
	// rolling it back stops the workflow it created.
	if _, err := p.prd.Rollback(ctx, first.ID, "rina", ""); err != nil {
		t.Fatal(err)
	}
	if p.prdStore.workflows["wf-orders"].Active {
		t.Fatal("rolling back the promotion that created the workflow left it running")
	}
}

func TestAutoRollbackWhenTheDeployFails(t *testing.T) {
	ctx := context.Background()
	p, _, second := deployTwice(t)

	if n, err := p.prd.CheckDeploys(ctx); err != nil || n != 0 {
		t.Fatalf("healthy: rolled back %d, %v", n, err)
	}
	wf := p.prdStore.workflows["wf-orders"]
	wf.TotalErrors, wf.TotalProcessed = second.ErrorsAtDeploy+50, second.ProcessedAtDeploy+100
	p.prdStore.workflows["wf-orders"] = wf
	n, err := p.prd.CheckDeploys(ctx)
	if err != nil || n != 1 {
		t.Fatalf("failing: rolled back %d, %v", n, err)
	}
	got, _ := p.prd.Promotions(ctx)
	if got[0].ID != second.ID || got[0].Status != StatusRolledBack || !strings.Contains(got[0].Reason, "50 of 150") {
		t.Fatalf("promotion = %+v", got[0])
	}
}

func TestAutoRollbackLeavesOldDeploysAndOptOutsAlone(t *testing.T) {
	ctx := context.Background()
	p, _, _ := deployTwice(t)
	wf := p.prdStore.workflows["wf-orders"]
	wf.Status = "Error: connection refused"
	p.prdStore.workflows["wf-orders"] = wf

	p.prd.now = func() time.Time { return time.Now().Add(RollbackWindow + time.Minute) }
	if n, _ := p.prd.CheckDeploys(ctx); n != 0 {
		t.Fatalf("rolled back a deploy older than the window")
	}
	p.prd.now = time.Now
	if _, err := p.prd.SetRules(ctx, Rules{Approval: ApproveAnyAdmin, AutoRollback: false}); err != nil {
		t.Fatal(err)
	}
	if n, _ := p.prd.CheckDeploys(ctx); n != 0 {
		t.Fatalf("rolled back with auto-rollback switched off")
	}
	if _, err := p.prd.SetRules(ctx, DefaultRules()); err != nil {
		t.Fatal(err)
	}
	if n, _ := p.prd.CheckDeploys(ctx); n != 1 {
		t.Fatalf("an engine error inside the window was not rolled back")
	}
}

func TestReferences(t *testing.T) {
	secretNames, envNames := references(ordersWorkflow())
	if !slices.Equal(secretNames, []string{"CRM_KEY"}) || !slices.Equal(envNames, []string{"REGION"}) {
		t.Fatalf("secrets %v envs %v", secretNames, envNames)
	}
}

func TestDiffIgnoresMovingNodes(t *testing.T) {
	a := ordersWorkflow()
	b := ordersWorkflow()
	b.Nodes[0].X, b.Nodes[0].Y = 400, 120
	b.Status, b.WorkerID = "", ""
	if got := Diff(&a, b); len(got) != 0 {
		t.Fatalf("Diff = %+v", got)
	}
	b.Nodes = b.Nodes[1:]
	b.Edges = b.Edges[1:]
	b.MaxRetries = 5
	got := Diff(&a, b)
	var what []string
	for _, c := range got {
		what = append(what, c.Kind+" "+c.What)
	}
	want := []string{"removed source node Orders", "changed Links between nodes: 0 added, 1 removed", "changed Setting max_retries"}
	if !slices.Equal(what, want) {
		t.Fatalf("Diff = %v, want %v", what, want)
	}
}
