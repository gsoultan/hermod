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
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
)

// linkVHost links staging's vhost here to production's vhost there.
func (p *pair) linkVHost(t *testing.T, here, there string) Link {
	t.Helper()
	ctx := context.Background()
	code, _, err := p.prd.NewPairingCode(ctx, "", there, "rina")
	if err != nil {
		t.Fatal(err)
	}
	l, err := p.stg.LinkWith(ctx, LinkRequest{URL: "https://prod.example.com", Code: code, SelfURL: "https://stg.example.com", VHost: here}, "gembit")
	if err != nil {
		t.Fatalf("LinkWith: %v", err)
	}
	return l
}

func TestAPairingCodeIsForOneVHostAndCreatesItWhenMissing(t *testing.T) {
	ctx := context.Background()
	p := newPair(t)
	p.prdStore.vhosts = []storage.VHost{{ID: "v-hr", Name: "hr"}}

	l := p.linkVHost(t, "Marketing", "marketing")
	if l.VHost != "Marketing" || l.TargetVHost != "marketing" || !l.VHostCreated {
		t.Fatalf("link = %+v, want Marketing to marketing, created there", l)
	}
	if !p.prdStore.hasVHost("marketing") {
		t.Fatal("production did not create the missing vhost")
	}
	peers, _ := p.prd.Peers(ctx)
	if len(peers) != 1 || peers[0].VHost != "marketing" || peers[0].FromVHost != "Marketing" {
		t.Fatalf("peers = %+v", peers)
	}

	hr := p.linkVHost(t, "HR", "hr")
	if hr.VHostCreated || hr.TargetVHost != "hr" {
		t.Fatalf("an existing vhost was reported as created: %+v", hr)
	}
	if n := len(p.prdStore.vhosts); n != 2 {
		t.Fatalf("production has %d vhosts, want 2", n)
	}
	// The shared default vhost always exists, row or not.
	if l := p.linkVHost(t, "default", "default"); l.VHostCreated || p.prdStore.hasVHost("default") {
		t.Fatalf("the default vhost was created: %+v", l)
	}
}

func TestAPairingCodeNeedsAValidVHost(t *testing.T) {
	ctx := context.Background()
	p := newPair(t)
	for _, vhost := range []string{"", " ", "has space", "slash/", "-dash", string(make([]byte, 70))} {
		if _, _, err := p.prd.NewPairingCode(ctx, "", vhost, "rina"); !errors.Is(err, ErrInvalid) {
			t.Errorf("vhost %q: err = %v, want ErrInvalid", vhost, err)
		}
	}
	code, _, _ := p.prd.NewPairingCode(ctx, "", "hr", "rina")
	if _, err := p.stg.LinkWith(ctx, LinkRequest{URL: "https://prod.example.com", Code: code, VHost: ""}, "gembit"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a link without a vhost here: err = %v, want ErrInvalid", err)
	}
}

func TestEachVHostHasItsOwnLink(t *testing.T) {
	ctx := context.Background()
	p := newPair(t)
	hr := p.linkVHost(t, "HR", "hr")
	if _, err := p.stg.SetMapping(ctx, hr.ID, map[string]string{"s-1": "p-1"}); err != nil {
		t.Fatal(err)
	}
	p.linkVHost(t, "Marketing", "marketing")
	again := p.linkVHost(t, "HR", "hr")

	links, _ := p.stg.Links(ctx)
	if len(links) != 2 {
		t.Fatalf("links = %+v, want one for HR and one for Marketing", links)
	}
	if again.Mapping["s-1"] != "p-1" {
		t.Fatalf("linking HR again lost its matched connections: %+v", again)
	}
	if token, _ := loadLinkToken(ctx, p.stgStore, hr.ID); token != "" {
		t.Fatal("the replaced link's token was kept")
	}
	if got, ok := p.stg.linkFor(links, "Marketing"); !ok || got.VHost != "Marketing" {
		t.Fatalf("linkFor(Marketing) = %+v, %v", got, ok)
	}
	if _, ok := p.stg.linkFor(links, "Sandbox"); ok {
		t.Fatal("a vhost with no link found one")
	}
}

func TestALinkedVHostSeesOnlyItsOwnConnections(t *testing.T) {
	ctx := context.Background()
	p := newPair(t)
	p.stgStore.sources["s-people"] = storage.Source{ID: "s-people", Name: "people-db", Type: "postgres", VHost: "HR"}
	p.stgStore.sources["s-leads"] = storage.Source{ID: "s-leads", Name: "leads-db", Type: "postgres", VHost: "Marketing"}
	p.prdStore.sources["p-people"] = storage.Source{ID: "p-people", Name: "people-db", Type: "postgres", VHost: "hr"}
	p.prdStore.sources["p-pay"] = storage.Source{ID: "p-pay", Name: "payroll-db", Type: "postgres", VHost: "payroll"}
	l := p.linkVHost(t, "HR", "hr")

	rows, err := p.stg.Matching(ctx, l.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != "s-people" || rows[0].TargetID != "p-people" || rows[0].Status != MatchMatched {
		t.Fatalf("rows = %+v, want only HR's connection, matched", rows)
	}
	if len(rows[0].Candidates) != 1 || rows[0].Candidates[0].ID != "p-people" {
		t.Fatalf("candidates = %+v, want only production's hr connections", rows[0].Candidates)
	}
	if _, err := p.stg.Matching(ctx, l.ID, "Marketing"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("matching another vhost's connections: err = %v, want ErrInvalid", err)
	}
}

// hrWorkflow is a workflow in staging's HR vhost.
func hrWorkflow(id, name, source string) storage.Workflow {
	return storage.Workflow{
		ID: id, Name: name, VHost: "HR",
		Nodes: []storage.WorkflowNode{{ID: "n1", Type: "source", RefID: source, Config: map[string]any{"label": "People"}}},
	}
}

func TestAPromotionLandsInTheLinkedVHost(t *testing.T) {
	ctx := context.Background()
	p := newPair(t)
	p.stgStore.sources["s-people"] = storage.Source{ID: "s-people", Name: "people-db", Type: "postgres", VHost: "HR"}
	p.prdStore.sources["p-people"] = storage.Source{ID: "p-people", Name: "people-db", Type: "postgres", VHost: "hr"}
	p.stgStore.saveVersion(hrWorkflow("wf-onb", "onboarding", "s-people"), "gembit", "first")
	l := p.linkVHost(t, "HR", "hr")

	sent, err := p.stg.Promote(ctx, l.ID, "wf-onb", "gembit", PromoteOptions{})
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}
	if sent.VHost != "HR" {
		t.Fatalf("sent = %+v, want staging's record of it under HR", sent)
	}
	if got, _ := p.prd.Promotions(ctx); len(got) != 1 || got[0].VHost != "hr" {
		t.Fatalf("production received %+v, want it to land in hr", got)
	}
	if _, err := p.prd.Approve(ctx, sent.ID, "rina"); err != nil {
		t.Fatal(err)
	}
	if wf := p.prdStore.workflows["wf-onb"]; wf.VHost != "hr" {
		t.Fatalf("deployed into vhost %q, want hr", wf.VHost)
	}

	// A workflow from another vhost cannot use HR's link.
	other := hrWorkflow("wf-mkt", "campaigns", "s-people")
	other.VHost = "Marketing"
	p.stgStore.saveVersion(other, "gembit", "first")
	if _, err := p.stg.Preview(ctx, l.ID, "wf-mkt", 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("previewing a Marketing workflow through HR's link: err = %v, want ErrInvalid", err)
	}
}

func TestALinkedVHostCannotReachIntoAnother(t *testing.T) {
	ctx := context.Background()
	p := newPair(t)
	p.prdStore.sources["p-pay"] = storage.Source{ID: "p-pay", Name: "payroll-db", Type: "postgres", VHost: "payroll"}
	p.prdStore.sources["p-people"] = storage.Source{ID: "p-people", Name: "people-db", Type: "postgres", VHost: "hr"}
	p.prdStore.workflows["wf-pay"] = storage.Workflow{ID: "wf-pay", Name: "payroll", VHost: "payroll"}
	peer := Peer{ID: "peer-hr", Name: "Staging", VHost: "hr"}

	// Overwriting a workflow that lives in another vhost.
	_, err := p.prd.Receive(ctx, peer, Submission{Workflow: storage.Workflow{ID: "wf-pay", Name: "payroll"}})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("overwriting payroll's workflow: err = %v, want ErrForbidden", err)
	}
	// Using a connection from another vhost.
	wf := hrWorkflow("wf-new", "new", "p-pay")
	if _, err := p.prd.Receive(ctx, peer, Submission{Workflow: wf}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("using payroll's connection: err = %v, want ErrInvalid", err)
	}
	res, err := p.prd.PeerCheck(ctx, peer, CheckRequest{WorkflowID: "wf-pay", Sources: []string{"p-pay", "p-people"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Current != nil || len(res.MissingSources) != 1 || res.MissingSources[0] != "p-pay" || res.Problem == "" {
		t.Fatalf("check = %+v, want payroll's workflow and connection hidden and the clash reported", res)
	}
	conns, _ := p.prd.PeerConnections(ctx, peer)
	if len(conns.Sources) != 1 || conns.Sources[0].ID != "p-people" {
		t.Fatalf("connections = %+v, want only hr's", conns)
	}

	// A peer linked before vhosts (no vhost) keeps sending anywhere.
	legacy := Peer{ID: "peer-old", Name: "Old staging"}
	got, err := p.prd.Receive(ctx, legacy, Submission{Workflow: storage.Workflow{ID: "wf-pay", Name: "payroll"}})
	if err != nil || got.VHost != "payroll" {
		t.Fatalf("legacy peer: %+v, %v", got, err)
	}
}

func TestRulesCanDifferByVHost(t *testing.T) {
	r := DefaultRules()
	r.ByVHost = map[string]VHostRules{"payroll": {Approval: ApproveAnyAdmin, MinSoakMinutes: 1440}}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := r.For("payroll"); got.Approval != ApproveAnyAdmin || got.MinSoakMinutes != 1440 || !got.AutoRollback {
		t.Fatalf("For(payroll) = %+v", got)
	}
	if got := r.For("hr"); got.Approval != ApproveAnyAdminExceptSender || got.MinSoakMinutes != 0 {
		t.Fatalf("For(hr) = %+v", got)
	}
	bad := DefaultRules()
	bad.ByVHost = map[string]VHostRules{"payroll": {Approval: "whoever"}}
	if err := bad.Validate(); err == nil {
		t.Fatal("a bad approval for one vhost was accepted")
	}
	bad.ByVHost = map[string]VHostRules{"no such/vhost": {Approval: ApproveAnyAdmin}}
	if err := bad.Validate(); err == nil {
		t.Fatal("a bad vhost name was accepted")
	}
}

func TestTheSendersVHostRuleApplies(t *testing.T) {
	ctx := context.Background()
	p := newPair(t)
	p.stgStore.sources["s-people"] = storage.Source{ID: "s-people", Name: "people-db", Type: "postgres", VHost: "HR"}
	p.prdStore.sources["p-people"] = storage.Source{ID: "p-people", Name: "people-db", Type: "postgres", VHost: "hr"}
	p.stgStore.saveVersion(hrWorkflow("wf-onb", "onboarding", "s-people"), "gembit", "first")
	r := DefaultRules()
	r.ByVHost = map[string]VHostRules{"hr": {Approval: ApproveNone}}
	if _, err := p.prd.SetRules(ctx, r); err != nil {
		t.Fatal(err)
	}
	l := p.linkVHost(t, "HR", "hr")
	sent, err := p.stg.Promote(ctx, l.ID, "wf-onb", "gembit", PromoteOptions{})
	if err != nil || sent.Status != StatusDeployed {
		t.Fatalf("hr deploys without approval: %+v, %v", sent, err)
	}
}
