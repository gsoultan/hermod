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

	"github.com/gsoultan/hermod/internal/storage"
)

// router is a Remote that reaches one loopback per address.
type router map[string]*loopback

func (r router) Pair(ctx context.Context, u string, req PairRequest) (PairResult, error) {
	return r[u].Pair(ctx, u, req)
}
func (r router) Info(ctx context.Context, u, token string) (PeerInfo, error) {
	return r[u].Info(ctx, u, token)
}
func (r router) Connections(ctx context.Context, u, token string) (Connections, error) {
	return r[u].Connections(ctx, u, token)
}
func (r router) Check(ctx context.Context, u, token string, req CheckRequest) (CheckResult, error) {
	return r[u].Check(ctx, u, token, req)
}
func (r router) Submit(ctx context.Context, u, token string, sub Submission) (Promotion, error) {
	return r[u].Submit(ctx, u, token, sub)
}
func (r router) SubmitRelease(ctx context.Context, u, token string, rs ReleaseSubmission) ([]Promotion, error) {
	return r[u].SubmitRelease(ctx, u, token, rs)
}
func (r router) Promotions(ctx context.Context, u, token string) ([]Promotion, error) {
	return r[u].Promotions(ctx, u, token)
}
func (r router) Unpair(ctx context.Context, u, token string) error { return r[u].Unpair(ctx, u, token) }

const (
	hrURL  = "https://hr.example.com"
	mktURL = "https://mkt.example.com"
)

// fleet is a staging instance whose HR vhost promotes to one production and
// whose Marketing vhost promotes to another.
type fleet struct {
	stg, hr, mkt                *Service
	stgStore, hrStore, mktStore *memStore
	remote                      router
	hrLink, mktLink             Link
}

// failingApplier refuses to store a workflow named "explodes".
type failingApplier struct{ memApplier }

func (a failingApplier) CreateWorkflowVersioned(ctx context.Context, wf storage.Workflow, by, msg string) (int, error) {
	if wf.Name == "explodes" {
		return 0, errors.New("disk full")
	}
	return a.memApplier.CreateWorkflowVersioned(ctx, wf, by, msg)
}

func newFleet(t *testing.T) *fleet {
	t.Helper()
	ctx := context.Background()
	f := &fleet{stgStore: newMemStore(), hrStore: newMemStore(), mktStore: newMemStore()}
	f.hr = NewService(f.hrStore, failingApplier{memApplier{f.hrStore}}, nil, nil, "1.21.0")
	f.mkt = NewService(f.mktStore, memApplier{f.mktStore}, nil, nil, "1.21.0")
	f.remote = router{hrURL: {target: f.hr}, mktURL: {target: f.mkt}}
	f.stg = NewService(f.stgStore, memApplier{f.stgStore}, f.remote, nil, "1.21.0")
	for svc, name := range map[*Service]string{f.stg: "Staging", f.hr: "Production HR", f.mkt: "Production Marketing"} {
		role := RoleProduction
		if svc == f.stg {
			role = RoleStaging
		}
		if _, err := svc.SetInstance(ctx, Instance{Name: name, Role: role}); err != nil {
			t.Fatal(err)
		}
	}
	f.stgStore.sources["s-people"] = storage.Source{ID: "s-people", Name: "people-db", Type: "postgres", VHost: "HR"}
	f.stgStore.sources["s-pay"] = storage.Source{ID: "s-pay", Name: "payroll-sftp", Type: "sftp", VHost: "HR"}
	f.stgStore.sources["s-leads"] = storage.Source{ID: "s-leads", Name: "leads-db", Type: "postgres", VHost: "Marketing"}
	f.hrStore.sources["p-people"] = storage.Source{ID: "p-people", Name: "people-db", Type: "postgres", VHost: "hr"}
	f.mktStore.sources["m-leads"] = storage.Source{ID: "m-leads", Name: "leads-db", Type: "postgres", VHost: "marketing"}

	f.stgStore.saveVersion(hrWorkflow("wf-onb", "onboarding", "s-people"), "gembit", "first")
	f.stgStore.saveVersion(hrWorkflow("wf-leave", "leave requests", "s-people"), "gembit", "first")
	f.stgStore.saveVersion(hrWorkflow("wf-pay", "payroll export", "s-pay"), "gembit", "first")
	camp := hrWorkflow("wf-camp", "campaign leads", "s-leads")
	camp.VHost = "Marketing"
	f.stgStore.saveVersion(camp, "gembit", "first")
	sandbox := hrWorkflow("wf-sand", "load test", "s-people")
	sandbox.VHost = "Sandbox"
	f.stgStore.saveVersion(sandbox, "gembit", "first")

	f.hrLink = f.connect(t, f.hr, hrURL, "HR", "hr")
	f.mktLink = f.connect(t, f.mkt, mktURL, "Marketing", "marketing")
	return f
}

func (f *fleet) connect(t *testing.T, prd *Service, u, here, there string) Link {
	t.Helper()
	ctx := context.Background()
	code, _, err := prd.NewPairingCode(ctx, "", there, "rina")
	if err != nil {
		t.Fatal(err)
	}
	l, err := f.stg.LinkWith(ctx, LinkRequest{URL: u, Code: code, VHost: here}, "gembit")
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func items(ids ...string) []ReleaseItem {
	out := make([]ReleaseItem, 0, len(ids))
	for _, id := range ids {
		out = append(out, ReleaseItem{WorkflowID: id})
	}
	return out
}

func TestAReleasePreviewGroupsWorkflowsByWhereTheyGo(t *testing.T) {
	f := newFleet(t)
	pv, err := f.stg.PreviewRelease(context.Background(), items("wf-onb", "wf-pay", "wf-camp", "wf-sand"))
	if err != nil {
		t.Fatal(err)
	}
	if pv.Ready || len(pv.Groups) != 2 {
		t.Fatalf("preview = %+v, want two groups and not ready", pv)
	}
	hr, mkt := pv.Groups[0], pv.Groups[1]
	if hr.LinkID != f.hrLink.ID || hr.VHost != "HR" || hr.TargetVHost != "hr" || len(hr.Previews) != 2 || hr.Ready {
		t.Fatalf("HR group = %+v", hr)
	}
	if !hr.Previews[0].Ready || hr.Previews[1].Ready || hr.Previews[1].WorkflowID != "wf-pay" {
		t.Fatalf("HR previews: onboarding should be ready and payroll blocked: %+v", hr.Previews)
	}
	if mkt.LinkID != f.mktLink.ID || !mkt.Ready || len(mkt.Previews) != 1 {
		t.Fatalf("Marketing group = %+v", mkt)
	}
	if len(pv.Unlinked) != 1 || pv.Unlinked[0].WorkflowID != "wf-sand" || pv.Unlinked[0].VHost != "Sandbox" {
		t.Fatalf("unlinked = %+v", pv.Unlinked)
	}
}

func TestAReleaseIsRefusedUntilEveryWorkflowIsReady(t *testing.T) {
	ctx := context.Background()
	f := newFleet(t)
	if _, err := f.stg.SendRelease(ctx, items("wf-onb", "wf-pay"), "gembit", PromoteOptions{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a release with a blocked workflow: err = %v, want ErrInvalid", err)
	}
	if ps, _ := f.hr.Promotions(ctx); len(ps) != 0 {
		t.Fatalf("part of a refused release arrived: %+v", ps)
	}
	if _, err := f.stg.SendRelease(ctx, nil, "gembit", PromoteOptions{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("an empty release: err = %v, want ErrInvalid", err)
	}
	if _, err := f.stg.SendRelease(ctx, items("wf-onb", "wf-onb"), "gembit", PromoteOptions{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("the same workflow twice: err = %v, want ErrInvalid", err)
	}
}

func TestOneClickSendsAReleaseToEachProduction(t *testing.T) {
	ctx := context.Background()
	f := newFleet(t)
	sent, err := f.stg.SendRelease(ctx, items("wf-onb", "wf-leave", "wf-camp"), "gembit", PromoteOptions{Note: "October", Start: true, AutoRollback: true})
	if err != nil {
		t.Fatalf("SendRelease: %v", err)
	}
	if len(sent) != 2 || len(sent[0].Promotions) != 2 || len(sent[1].Promotions) != 1 || sent[0].ReleaseID == sent[1].ReleaseID {
		t.Fatalf("sent = %+v", sent)
	}
	hr, _ := f.hr.Promotions(ctx)
	if len(hr) != 2 || hr[0].ReleaseID != sent[0].ReleaseID || hr[1].ReleaseID != sent[0].ReleaseID || hr[0].Note != "October" {
		t.Fatalf("Production HR received %+v", hr)
	}
	if mkt, _ := f.mkt.Promotions(ctx); len(mkt) != 1 || mkt[0].WorkflowID != "wf-camp" || mkt[0].VHost != "marketing" {
		t.Fatalf("Production Marketing received %+v", mkt)
	}
	log, _ := f.stg.Sent(ctx)
	if len(log) != 3 || log[0].ReleaseID == "" {
		t.Fatalf("sent log = %+v", log)
	}
}

func TestApprovingAReleaseDeploysAllOfIt(t *testing.T) {
	ctx := context.Background()
	f := newFleet(t)
	sent, err := f.stg.SendRelease(ctx, items("wf-onb", "wf-leave"), "gembit", PromoteOptions{Start: true})
	if err != nil {
		t.Fatal(err)
	}
	one := sent[0].Promotions[1].ID
	if _, err := f.hr.Approve(ctx, one, "gembit"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("the sender approving: err = %v, want ErrForbidden", err)
	}
	if _, err := f.hr.Approve(ctx, one, "rina"); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	ps, _ := f.hr.Promotions(ctx)
	for _, p := range ps {
		if p.Status != StatusDeployed {
			t.Fatalf("promotion %s is %s, want the whole release deployed", p.WorkflowName, p.Status)
		}
	}
	if !f.hrStore.workflows["wf-onb"].Active || !f.hrStore.workflows["wf-leave"].Active {
		t.Fatal("the release's workflows are not running")
	}
	// The sender sees what happened there.
	log, _ := f.stg.Sent(ctx)
	for _, p := range log {
		if p.Status != StatusDeployed {
			t.Fatalf("staging still shows %s as %s", p.WorkflowName, p.Status)
		}
	}
}

func TestRejectingAReleaseRejectsAllOfIt(t *testing.T) {
	ctx := context.Background()
	f := newFleet(t)
	sent, _ := f.stg.SendRelease(ctx, items("wf-onb", "wf-leave"), "gembit", PromoteOptions{})
	if _, err := f.hr.Reject(ctx, sent[0].Promotions[0].ID, "rina", "not this week"); err != nil {
		t.Fatal(err)
	}
	ps, _ := f.hr.Promotions(ctx)
	for _, p := range ps {
		if p.Status != StatusRejected || p.Reason != "not this week" {
			t.Fatalf("promotion = %+v", p)
		}
	}
}

func TestAReleaseThatFailsHalfwayIsUndone(t *testing.T) {
	ctx := context.Background()
	f := newFleet(t)
	boom := hrWorkflow("wf-boom", "explodes", "s-people")
	f.stgStore.saveVersion(boom, "gembit", "first")
	sent, err := f.stg.SendRelease(ctx, items("wf-onb", "wf-boom", "wf-leave"), "gembit", PromoteOptions{Start: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.hr.Approve(ctx, sent[0].Promotions[0].ID, "rina"); err == nil {
		t.Fatal("a release whose deploy failed reported success")
	}
	status := map[string]string{}
	ps, _ := f.hr.Promotions(ctx)
	for _, p := range ps {
		status[p.WorkflowID] = p.Status
	}
	want := map[string]string{"wf-onb": StatusRolledBack, "wf-boom": StatusFailed, "wf-leave": StatusFailed}
	for id, s := range want {
		if status[id] != s {
			t.Errorf("%s is %s, want %s", id, status[id], s)
		}
	}
	if f.hrStore.workflows["wf-onb"].Active {
		t.Fatal("the workflow deployed before the failure was left running")
	}
	if _, ok := f.hrStore.workflows["wf-leave"]; ok {
		t.Fatal("a workflow after the failure was deployed")
	}
}

func TestRollingBackAReleaseUndoesAllOfIt(t *testing.T) {
	ctx := context.Background()
	f := newFleet(t)
	sent, _ := f.stg.SendRelease(ctx, items("wf-onb", "wf-leave"), "gembit", PromoteOptions{Start: true, AutoRollback: true})
	if _, err := f.hr.Approve(ctx, sent[0].Promotions[0].ID, "rina"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.hr.Rollback(ctx, sent[0].Promotions[1].ID, "rina", ""); err != nil {
		t.Fatal(err)
	}
	ps, _ := f.hr.Promotions(ctx)
	for _, p := range ps {
		if p.Status != StatusRolledBack {
			t.Fatalf("%s is %s after rolling back its release", p.WorkflowName, p.Status)
		}
	}
	if f.hrStore.workflows["wf-onb"].Active || f.hrStore.workflows["wf-leave"].Active {
		t.Fatal("rolling back the release that created them left them running")
	}
}

func TestAutoRollbackTakesTheWholeRelease(t *testing.T) {
	ctx := context.Background()
	f := newFleet(t)
	sent, _ := f.stg.SendRelease(ctx, items("wf-onb", "wf-leave"), "gembit", PromoteOptions{Start: true, AutoRollback: true})
	if _, err := f.hr.Approve(ctx, sent[0].Promotions[0].ID, "rina"); err != nil {
		t.Fatal(err)
	}
	wf := f.hrStore.workflows["wf-leave"]
	wf.Status = "Error: connection refused"
	f.hrStore.workflows["wf-leave"] = wf
	if _, err := f.hr.CheckDeploys(ctx); err != nil {
		t.Fatal(err)
	}
	ps, _ := f.hr.Promotions(ctx)
	for _, p := range ps {
		if p.Status != StatusRolledBack || !strings.Contains(p.Reason, "automatically") {
			t.Fatalf("%s = %s (%s), want the healthy one rolled back with the failing one", p.WorkflowName, p.Status, p.Reason)
		}
	}
}

func TestReceivingTheSameReleaseTwiceKeepsOne(t *testing.T) {
	ctx := context.Background()
	f := newFleet(t)
	peers, _ := f.hr.Peers(ctx)
	sub := Submission{Workflow: hrWorkflow("wf-x", "x", "p-people"), Sender: "gembit"}
	rs := ReleaseSubmission{ReleaseID: "11111111-2222-3333-4444-555555555555", Items: []Submission{sub}}
	first, err := f.hr.ReceiveRelease(ctx, peers[0], rs)
	if err != nil {
		t.Fatal(err)
	}
	again, err := f.hr.ReceiveRelease(ctx, peers[0], rs)
	if err != nil || len(again) != 1 || again[0].ID != first[0].ID {
		t.Fatalf("again = %+v, %v; want the first one back", again, err)
	}
	if ps, _ := f.hr.Promotions(ctx); len(ps) != 1 {
		t.Fatalf("promotions = %d, want 1", len(ps))
	}
}

func TestAReleaseWithOneBadWorkflowStoresNothing(t *testing.T) {
	ctx := context.Background()
	f := newFleet(t)
	peers, _ := f.hr.Peers(ctx)
	good := Submission{Workflow: hrWorkflow("wf-x", "x", "p-people")}
	bad := Submission{Workflow: hrWorkflow("wf-y", "y", "p-missing")}
	_, err := f.hr.ReceiveRelease(ctx, peers[0], ReleaseSubmission{ReleaseID: "r-1", Items: []Submission{good, bad}})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
	if ps, _ := f.hr.Promotions(ctx); len(ps) != 0 {
		t.Fatalf("part of a bad release was stored: %+v", ps)
	}
	if _, err := f.hr.ReceiveRelease(ctx, peers[0], ReleaseSubmission{ReleaseID: "r-2"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("an empty release: err = %v, want ErrInvalid", err)
	}
	many := ReleaseSubmission{ReleaseID: "r-3", Items: slices.Repeat([]Submission{good}, maxReleaseItems+1)}
	if _, err := f.hr.ReceiveRelease(ctx, peers[0], many); !errors.Is(err, ErrInvalid) {
		t.Fatalf("too big a release: err = %v, want ErrInvalid", err)
	}
}

func TestAnOlderProductionGetsTheWorkflowsOneByOne(t *testing.T) {
	ctx := context.Background()
	f := newFleet(t)
	f.remote[hrURL].old = true
	sent, err := f.stg.SendRelease(ctx, items("wf-onb", "wf-leave"), "gembit", PromoteOptions{})
	if err != nil {
		t.Fatalf("SendRelease: %v", err)
	}
	if len(sent) != 1 || len(sent[0].Promotions) != 2 {
		t.Fatalf("sent = %+v", sent)
	}
	if ps, _ := f.hr.Promotions(ctx); len(ps) != 2 {
		t.Fatalf("production got %d promotions, want 2", len(ps))
	}
}

func TestReadyListsWhatChangedSinceTheLastPromotion(t *testing.T) {
	ctx := context.Background()
	f := newFleet(t)
	lanes, err := f.stg.Ready(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ids := func(l ReadyLane) []string {
		var out []string
		for _, w := range l.Workflows {
			out = append(out, w.ID)
		}
		return out
	}
	if len(lanes) != 2 || lanes[0].VHost != "HR" || !slices.Equal(ids(lanes[0]), []string{"wf-leave", "wf-onb", "wf-pay"}) {
		t.Fatalf("lanes = %+v", lanes)
	}
	if _, err := f.stg.SendRelease(ctx, items("wf-onb"), "gembit", PromoteOptions{}); err != nil {
		t.Fatal(err)
	}
	lanes, _ = f.stg.Ready(ctx)
	if !slices.Equal(ids(lanes[0]), []string{"wf-leave", "wf-pay"}) {
		t.Fatalf("after promoting onboarding: %v", ids(lanes[0]))
	}
	f.stgStore.saveVersion(hrWorkflow("wf-onb", "onboarding v2", "s-people"), "gembit", "second")
	lanes, _ = f.stg.Ready(ctx)
	if !slices.Equal(ids(lanes[0]), []string{"wf-leave", "wf-onb", "wf-pay"}) || lanes[0].Workflows[1].LastSentVersion != 1 || lanes[0].Workflows[1].Version != 2 {
		t.Fatalf("after a new version: %+v", lanes[0].Workflows)
	}
}

func TestAWorkflowWithNoVHostIsReadyForTheDefaultLink(t *testing.T) {
	p := newPair(t)
	wf := ordersWorkflow()
	wf.VHost = ""
	p.stgStore.saveVersion(wf, "gembit", "first")
	p.link(t)
	lanes, err := p.stg.Ready(context.Background())
	if err != nil || len(lanes) != 1 || len(lanes[0].Workflows) != 1 || lanes[0].Workflows[0].VHost != "default" {
		t.Fatalf("lanes = %+v, %v", lanes, err)
	}
}
