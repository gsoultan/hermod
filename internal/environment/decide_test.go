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
)

// decidable is promotable with production letting staging's admins decide,
// and staging's link knowing it may.
func decidable(t *testing.T) (*pair, Link) {
	t.Helper()
	ctx := context.Background()
	p, l := promotable(t)
	peers, err := p.prd.Peers(ctx)
	if err != nil || len(peers) != 1 {
		t.Fatalf("peers = %+v, %v", peers, err)
	}
	if _, err := p.prd.SetPeerDecide(ctx, peers[0].ID, true); err != nil {
		t.Fatalf("SetPeerDecide: %v", err)
	}
	l, _, err = p.stg.TestLink(ctx, l.ID)
	if err != nil {
		t.Fatalf("TestLink: %v", err)
	}
	if !l.Decide {
		t.Fatalf("the link did not learn it may decide: %+v", l)
	}
	return p, l
}

func TestAnAdminOnStagingApprovesWithoutSigningInToProduction(t *testing.T) {
	ctx := context.Background()
	p, l := decidable(t)
	sent, err := p.stg.Promote(ctx, l.ID, "wf-orders", "gembit", PromoteOptions{Start: true})
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.stg.DecideSent(ctx, sent.ID, DecisionApprove, "rina", "")
	if err != nil {
		t.Fatalf("DecideSent: %v", err)
	}
	if got.Status != StatusDeployed || got.DecidedBy != "rina (via Staging)" {
		t.Fatalf("promotion = %+v", got)
	}
	if wf, ok := p.prdStore.workflows["wf-orders"]; !ok || !wf.Active {
		t.Fatalf("production did not deploy it: %+v", wf)
	}
	there, _ := p.prd.Promotions(ctx)
	if len(there) != 1 || there[0].DecidedBy != "rina (via Staging)" {
		t.Fatalf("production's record = %+v", there)
	}
	log, _ := p.stg.Sent(ctx)
	if len(log) != 1 || log[0].Status != StatusDeployed || log[0].DecidedBy != "rina (via Staging)" {
		t.Fatalf("sent log = %+v", log)
	}
}

func TestTheSenderCannotApproveTheirOwnFromStaging(t *testing.T) {
	ctx := context.Background()
	p, l := decidable(t)
	sent, err := p.stg.Promote(ctx, l.ID, "wf-orders", "gembit", PromoteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.stg.DecideSent(ctx, sent.ID, DecisionApprove, "Gembit", ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
	if there, _ := p.prd.Promotions(ctx); there[0].Status != StatusPending {
		t.Fatalf("status = %s, want pending", there[0].Status)
	}
}

func TestDecidingFromStagingNeedsProductionsPermission(t *testing.T) {
	ctx := context.Background()
	p, l := promotable(t)
	sent, err := p.stg.Promote(ctx, l.ID, "wf-orders", "gembit", PromoteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range []string{DecisionApprove, DecisionReject} {
		if _, err := p.stg.DecideSent(ctx, sent.ID, a, "rina", ""); !errors.Is(err, ErrForbidden) {
			t.Fatalf("%s: err = %v, want ErrForbidden", a, err)
		}
	}
	// Production refuses it too, should staging ask anyway.
	peers, _ := p.prd.Peers(ctx)
	if _, err := p.prd.PeerDecide(ctx, peers[0], Decision{PromotionID: sent.ID, Action: DecisionApprove, By: "rina"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("production: err = %v, want ErrForbidden", err)
	}
	if there, _ := p.prd.Promotions(ctx); there[0].Status != StatusPending {
		t.Fatalf("status = %s, want pending", there[0].Status)
	}
}

func TestRejectAndUndoFromStaging(t *testing.T) {
	ctx := context.Background()
	p, l := decidable(t)
	first, err := p.stg.Promote(ctx, l.ID, "wf-orders", "gembit", PromoteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.stg.DecideSent(ctx, first.ID, DecisionReject, "rina", "  not this week ")
	if err != nil || got.Status != StatusRejected || got.Reason != "not this week" || got.DecidedBy != "rina (via Staging)" {
		t.Fatalf("reject = %+v, %v", got, err)
	}

	second, err := p.stg.Promote(ctx, l.ID, "wf-orders", "gembit", PromoteOptions{Start: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.stg.DecideSent(ctx, second.ID, DecisionApprove, "rina", ""); err != nil {
		t.Fatal(err)
	}
	got, err = p.stg.DecideSent(ctx, second.ID, DecisionRollback, "rina", "")
	if err != nil || got.Status != StatusRolledBack {
		t.Fatalf("rollback = %+v, %v", got, err)
	}
	if p.prdStore.workflows["wf-orders"].Active {
		t.Fatal("the workflow it created still runs after the undo")
	}
	if _, err := p.stg.DecideSent(ctx, second.ID, "delete", "rina", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown action: err = %v, want ErrInvalid", err)
	}
}

func TestAPeerCannotDecideAnotherPeersPromotion(t *testing.T) {
	ctx := context.Background()
	p, l := decidable(t)
	sent, err := p.stg.Promote(ctx, l.ID, "wf-orders", "gembit", PromoteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	other := Peer{ID: "someone-else", Name: "Other", VHost: "default", Decide: true}
	if _, err := p.prd.PeerDecide(ctx, other, Decision{PromotionID: sent.ID, Action: DecisionApprove, By: "rina"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if _, err := p.stg.DecideSent(ctx, "not-sent-from-here", DecisionApprove, "rina", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown promotion: err = %v, want ErrNotFound", err)
	}
}

func TestAPairingCodeCanLetTheOtherSideDecide(t *testing.T) {
	ctx := context.Background()
	p := newPair(t)
	code, _, err := p.prd.NewPairingCode(ctx, "", "default", "rina", AllowDeciding())
	if err != nil {
		t.Fatal(err)
	}
	l, err := p.stg.LinkWith(ctx, LinkRequest{URL: "https://prod.example.com", Code: code, SelfURL: "https://stg.example.com", VHost: "default"}, "gembit")
	if err != nil {
		t.Fatal(err)
	}
	if !l.Decide {
		t.Fatalf("link = %+v, want Decide", l)
	}
	peers, _ := p.prd.Peers(ctx)
	if !peers[0].Decide {
		t.Fatalf("peer = %+v, want Decide", peers[0])
	}
	// Taking it back is seen the next time staging checks the link.
	if _, err := p.prd.SetPeerDecide(ctx, peers[0].ID, false); err != nil {
		t.Fatal(err)
	}
	if l, _, _ = p.stg.TestLink(ctx, l.ID); l.Decide {
		t.Fatalf("link still says it may decide: %+v", l)
	}
}
