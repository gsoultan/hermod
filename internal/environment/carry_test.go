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

// alertsWorkflow sends orders to a Slack sink that production does not have.
func alertsWorkflow() storage.Workflow {
	wf := ordersWorkflow()
	wf.ID, wf.Name = "wf-alerts", "order-alerts"
	wf.Nodes = append(wf.Nodes, storage.WorkflowNode{ID: "n4", Type: "sink", RefID: "k-slack", Config: map[string]any{"label": "Alerts"}})
	wf.Edges = append(wf.Edges, storage.WorkflowEdge{ID: "e3", SourceID: "n2", TargetID: "n4"})
	return wf
}

// carrying is promotable plus staging's Slack sink, which production lacks,
// with the credential settings given.
func carrying(t *testing.T, cfg map[string]string) (*pair, Link, *memVault) {
	t.Helper()
	p, l, vault := vaulted(t)
	p.stgStore.sinks["k-slack"] = storage.Sink{ID: "k-slack", Name: "slack-alerts", Type: "slack", VHost: "default", Config: cfg}
	p.stgStore.saveVersion(alertsWorkflow(), "gembit", "first")
	return p, l, vault
}

// prdPeer is the peer production keeps for staging.
func (p *pair) prdPeer(t *testing.T) Peer {
	t.Helper()
	peers, err := loadPeers(context.Background(), p.prdStore)
	if err != nil || len(peers) == 0 {
		t.Fatalf("peers = %v, %v", peers, err)
	}
	return peers[0]
}

func row(rows []ConnectionRow, id string) (ConnectionRow, bool) {
	i := slices.IndexFunc(rows, func(r ConnectionRow) bool { return r.ID == id })
	if i < 0 {
		return ConnectionRow{}, false
	}
	return rows[i], true
}

func TestASinkProductionLacksIsCreatedThereInsteadOfBlocking(t *testing.T) {
	p, l, _ := carrying(t, map[string]string{"channel": "#orders", "token": "{{secret.SLACK_TOKEN}}"})
	pv, err := p.stg.Preview(context.Background(), l.ID, "wf-alerts", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !pv.Ready {
		t.Fatalf("not ready: %+v", pv.Checks)
	}
	r, ok := row(pv.Connections, "k-slack")
	if !ok || r.Status != ConnCreate || r.Kind != "sink" || r.Name != "slack-alerts" {
		t.Fatalf("slack row = %+v (found %v)", r, ok)
	}
	if !slices.Equal(r.Settings, []Setting{{Key: "channel", Value: "#orders"}, {Key: "token", Value: "{{secret.SLACK_TOKEN}}", Secret: true}}) {
		t.Fatalf("settings = %+v", r.Settings)
	}
	if wh, _ := row(pv.Connections, "k-wh"); wh.Status != ConnUses || wh.TargetName != "warehouse" {
		t.Fatalf("warehouse row = %+v", wh)
	}
	if !slices.Contains(pv.MissingSecrets, "SLACK_TOKEN") || !slices.Equal(pv.SecretUses["SLACK_TOKEN"], []string{"sink slack-alerts"}) {
		t.Fatalf("missing %v, uses %v", pv.MissingSecrets, pv.SecretUses)
	}
}

func TestACredentialTypedInPlainTextKeepsTheConnectionHere(t *testing.T) {
	p, l, _ := carrying(t, map[string]string{"channel": "#orders", "token": "xoxb-plain"})
	pv, err := p.stg.Preview(context.Background(), l.ID, "wf-alerts", 0)
	if err != nil {
		t.Fatal(err)
	}
	if pv.Ready {
		t.Fatal("ready with a plain credential to carry")
	}
	r, _ := row(pv.Connections, "k-slack")
	if r.Status != ConnBlocked || !slices.Equal(r.PlainKeys, []string{"token"}) {
		t.Fatalf("slack row = %+v", r)
	}
	for _, s := range r.Settings {
		if strings.Contains(s.Value, "xoxb") {
			t.Fatal("the preview shows the plain credential")
		}
	}
}

func TestApprovingCreatesTheCarriedSinkWithItsSecret(t *testing.T) {
	ctx := context.Background()
	p, l, vault := carrying(t, map[string]string{"channel": "#orders", "token": "{{secret.SLACK_TOKEN}}"})
	sent, err := p.stg.Promote(ctx, l.ID, "wf-alerts", "gembit", PromoteOptions{Start: true,
		Settings: map[string]map[string]string{"k-slack": {"channel": "#orders-prod"}}})
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}
	got, err := p.prd.promotion(ctx, sent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Connections) != 1 || got.Connections[0].ID != "k-slack" || !slices.Contains(got.Secrets, "SLACK_TOKEN") {
		t.Fatalf("promotion = %+v", got)
	}
	if _, err := p.prdStore.GetSink(ctx, "k-slack"); err == nil {
		t.Fatal("the sink was created before anyone approved")
	}

	if _, err := p.prd.ApproveWith(ctx, sent.ID, "rina", map[string]string{"SLACK_TOKEN": "xoxb-prod"}); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	snk, err := p.prdStore.GetSink(ctx, "k-slack")
	if err != nil {
		t.Fatalf("sink not created: %v", err)
	}
	if snk.Name != "slack-alerts" || snk.Type != "slack" || snk.VHost != "default" || !snk.Active ||
		snk.Config["channel"] != "#orders-prod" || snk.Config["token"] != "{{secret.SLACK_TOKEN}}" {
		t.Fatalf("sink = %+v", snk)
	}
	if v := vault.values["default/SLACK_TOKEN"]; v != "xoxb-prod" {
		t.Fatalf("SLACK_TOKEN = %q", v)
	}
	wf, err := p.prdStore.GetWorkflow(ctx, "wf-alerts")
	if err != nil || !slices.ContainsFunc(wf.Nodes, func(n storage.WorkflowNode) bool { return n.RefID == "k-slack" }) {
		t.Fatalf("workflow = %+v, %v", wf, err)
	}
}

func TestTheNextPromotionReusesTheSinkItCreated(t *testing.T) {
	ctx := context.Background()
	p, l, _ := carrying(t, map[string]string{"channel": "#orders", "token": "{{secret.SLACK_TOKEN}}"})
	sent, err := p.stg.Promote(ctx, l.ID, "wf-alerts", "gembit", PromoteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.prd.Approve(ctx, sent.ID, "rina"); err != nil {
		t.Fatal(err)
	}
	pv, err := p.stg.Preview(ctx, l.ID, "wf-alerts", 0)
	if err != nil {
		t.Fatal(err)
	}
	if r, _ := row(pv.Connections, "k-slack"); r.Status != ConnUses || r.TargetID != "k-slack" {
		t.Fatalf("slack row = %+v", r)
	}
}

func TestASettingChangedForProductionCannotSmuggleACredential(t *testing.T) {
	p, l, _ := carrying(t, map[string]string{"channel": "#orders", "token": "{{secret.SLACK_TOKEN}}"})
	_, err := p.stg.Promote(context.Background(), l.ID, "wf-alerts", "gembit", PromoteOptions{
		Settings: map[string]map[string]string{"k-slack": {"token": "xoxb-plain"}}})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "plain text") {
		t.Fatalf("err = %v, want ErrInvalid for the plain token", err)
	}
}

func TestProductionRefusesACarriedConnectionWithAPlainCredential(t *testing.T) {
	p, _, _ := carrying(t, nil)
	peer := p.prdPeer(t)
	sub := Submission{Workflow: alertsWorkflow(), FromVersion: 1, Sender: "gembit",
		Connections: []CarriedConnection{{Kind: "sink", ID: "k-slack", Name: "slack-alerts", Type: "slack",
			Config: map[string]string{"token": "xoxb-plain"}}}}
	sub.Workflow.Nodes = slices.DeleteFunc(sub.Workflow.Nodes, func(n storage.WorkflowNode) bool { return n.Type == "source" || n.ID == "n2" || n.ID == "n3" })
	if _, err := p.prd.Receive(context.Background(), peer, sub); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "plain text") {
		t.Fatalf("err = %v, want ErrInvalid for the plain token", err)
	}
}

func TestMovingAPlainCredentialToASecretLetsTheConnectionTravel(t *testing.T) {
	ctx := context.Background()
	p, l, _ := carrying(t, map[string]string{"channel": "#orders", "token": "xoxb-plain"})
	here := newVault()
	p.stg.secrets = here

	if err := p.stg.MoveToSecret(ctx, "sink", "k-slack", "token", "SLACK_TOKEN", "gembit"); err != nil {
		t.Fatalf("MoveToSecret: %v", err)
	}
	if snk, _ := p.stgStore.GetSink(ctx, "k-slack"); snk.Config["token"] != "{{secret.SLACK_TOKEN}}" {
		t.Fatalf("token setting = %q", snk.Config["token"])
	}
	if here.values["default/SLACK_TOKEN"] != "xoxb-plain" {
		t.Fatalf("staging's SLACK_TOKEN = %q", here.values["default/SLACK_TOKEN"])
	}
	pv, err := p.stg.Preview(ctx, l.ID, "wf-alerts", 0)
	if err != nil {
		t.Fatal(err)
	}
	if r, _ := row(pv.Connections, "k-slack"); r.Status != ConnCreate || !pv.Ready {
		t.Fatalf("slack row = %+v, ready %v", r, pv.Ready)
	}
}

func TestMovingToASecretNeverOverwritesOne(t *testing.T) {
	ctx := context.Background()
	p, _, _ := carrying(t, map[string]string{"token": "xoxb-plain"})
	here := newVault()
	here.values["default/SLACK_TOKEN"] = "older"
	p.stg.secrets = here
	if err := p.stg.MoveToSecret(ctx, "sink", "k-slack", "token", "SLACK_TOKEN", "gembit"); !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	if snk, _ := p.stgStore.GetSink(ctx, "k-slack"); snk.Config["token"] != "xoxb-plain" {
		t.Fatalf("token setting changed to %q", snk.Config["token"])
	}
	if err := p.stg.MoveToSecret(ctx, "sink", "k-slack", "channel", "CHANNEL", "gembit"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("moving a setting that is no credential: err = %v, want ErrInvalid", err)
	}
}
