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

// connect links staging's orders vhost to production with a code that
// lets staging add more vhosts on its own when addVHosts is set.
func (p *pair) connect(t *testing.T, addVHosts bool) Link {
	t.Helper()
	ctx := context.Background()
	var opts []PairingOption
	if addVHosts {
		opts = append(opts, AllowAddingVHosts())
	}
	code, _, err := p.prd.NewPairingCode(ctx, "", "orders", "rina", opts...)
	if err != nil {
		t.Fatal(err)
	}
	l, err := p.stg.LinkWith(ctx, LinkRequest{URL: "https://prod.example.com", Code: code, SelfURL: "https://stg.example.com", VHost: "orders"}, "gembit")
	if err != nil {
		t.Fatalf("LinkWith: %v", err)
	}
	return l
}

func TestAVHostIsAddedToAConnectionWithoutAPairingCode(t *testing.T) {
	ctx := context.Background()
	p := newPair(t)
	first := p.connect(t, true)

	added, err := p.stg.AddVHosts(ctx, first.ID, []VHostRoute{{VHost: "billing", TargetVHost: "billing-prod"}}, "gembit")
	if err != nil {
		t.Fatalf("AddVHosts: %v", err)
	}
	if len(added) != 1 || added[0].VHost != "billing" || added[0].TargetVHost != "billing-prod" || added[0].Pending || !added[0].VHostCreated {
		t.Fatalf("added = %+v", added)
	}
	if added[0].URL != first.URL || added[0].Name != first.Name {
		t.Fatalf("the new vhost is not on the same server: %+v", added[0])
	}
	if !p.prdStore.hasVHost("billing-prod") {
		t.Fatal("production did not create the vhost")
	}

	// Each vhost holds its own token, scoped to its own vhost there.
	tok, _ := loadLinkToken(ctx, p.stgStore, added[0].ID)
	firstTok, _ := loadLinkToken(ctx, p.stgStore, first.ID)
	if tok == "" || tok == firstTok {
		t.Fatalf("token = %q, first = %q; want a token of its own", tok, firstTok)
	}
	peer, err := p.prd.AuthenticatePeer(ctx, tok)
	if err != nil || peer.VHost != "billing-prod" || peer.FromVHost != "billing" {
		t.Fatalf("peer = %+v, %v", peer, err)
	}
	root, _ := p.prd.AuthenticatePeer(ctx, firstTok)
	if peer.Group != root.ID {
		t.Fatalf("peer group = %q, want the first peer %q", peer.Group, root.ID)
	}

	// The added vhost works like any link: its connections are its own.
	p.prdStore.sources["s-bill"] = storage.Source{ID: "s-bill", Name: "Ledger", Type: "postgres", VHost: "billing-prod"}
	p.prdStore.sources["s-ord"] = storage.Source{ID: "s-ord", Name: "Orders", Type: "postgres", VHost: "orders"}
	conns, err := p.remote.Connections(ctx, "", tok)
	if err != nil || len(conns.Sources) != 1 || conns.Sources[0].ID != "s-bill" {
		t.Fatalf("connections = %+v, %v", conns, err)
	}
}

func TestAddingAVHostAlreadyConnectedIsRefused(t *testing.T) {
	ctx := context.Background()
	p := newPair(t)
	first := p.connect(t, true)
	if _, err := p.stg.AddVHosts(ctx, first.ID, []VHostRoute{{VHost: "orders", TargetVHost: "orders"}}, "gembit"); !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict for a vhost here that already has a link", err)
	}
	if _, err := p.stg.AddVHosts(ctx, first.ID, []VHostRoute{{VHost: "billing", TargetVHost: "bad name!"}}, "gembit"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid for a bad vhost name", err)
	}
	// Production refuses a second route into a vhost the connection already sends into.
	if _, err := p.prd.AddPeerVHost(ctx, mustPeer(t, p, first), AddVHostRequest{VHost: "orders", FromVHost: "other"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
}

func mustPeer(t *testing.T, p *pair, l Link) Peer {
	t.Helper()
	tok, err := loadLinkToken(context.Background(), p.stgStore, l.ID)
	if err != nil {
		t.Fatal(err)
	}
	peer, err := p.prd.AuthenticatePeer(context.Background(), tok)
	if err != nil {
		t.Fatal(err)
	}
	return peer
}

func TestWithoutPermissionAnAddedVHostWaitsForAnAdminThere(t *testing.T) {
	ctx := context.Background()
	p := newPair(t)
	first := p.connect(t, false)

	added, err := p.stg.AddVHosts(ctx, first.ID, []VHostRoute{{VHost: "billing", TargetVHost: "billing"}}, "gembit")
	if err != nil {
		t.Fatalf("AddVHosts: %v", err)
	}
	if len(added) != 1 || !added[0].Pending {
		t.Fatalf("added = %+v, want one pending link", added)
	}
	if tok, _ := loadLinkToken(ctx, p.stgStore, added[0].ID); tok != "" {
		t.Fatal("a pending link holds a token")
	}
	if p.prdStore.hasVHost("billing") {
		t.Fatal("production created the vhost before anyone allowed it")
	}
	reqs, err := p.prd.VHostRequests(ctx)
	if err != nil || len(reqs) != 1 || reqs[0].VHost != "billing" || reqs[0].PeerName != "Staging" {
		t.Fatalf("requests = %+v, %v", reqs, err)
	}
	// A pending link cannot promote.
	if _, err := p.stg.Preview(ctx, added[0].ID, "wf", 0); err == nil {
		t.Fatal("a pending link previewed a promotion")
	}

	if err := p.prd.AllowVHostRequest(ctx, reqs[0].ID, "rina"); err != nil {
		t.Fatalf("Allow: %v", err)
	}
	// Staging picks the token up the next time it checks the link.
	l, _, err := p.stg.TestLink(ctx, added[0].ID)
	if err != nil || l.Pending {
		t.Fatalf("after allow: link = %+v, %v", l, err)
	}
	tok, _ := loadLinkToken(ctx, p.stgStore, added[0].ID)
	peer, err := p.prd.AuthenticatePeer(ctx, tok)
	if err != nil || peer.VHost != "billing" {
		t.Fatalf("peer = %+v, %v", peer, err)
	}
	if reqs, _ := p.prd.VHostRequests(ctx); len(reqs) != 0 {
		t.Fatalf("the request is still listed: %+v", reqs)
	}
}

func TestADeclinedVHostRequestTellsTheSender(t *testing.T) {
	ctx := context.Background()
	p := newPair(t)
	first := p.connect(t, false)
	added, err := p.stg.AddVHosts(ctx, first.ID, []VHostRoute{{VHost: "billing", TargetVHost: "billing"}}, "gembit")
	if err != nil {
		t.Fatal(err)
	}
	reqs, _ := p.prd.VHostRequests(ctx)
	if err := p.prd.DeclineVHostRequest(ctx, reqs[0].ID); err != nil {
		t.Fatal(err)
	}
	l, _, err := p.stg.TestLink(ctx, added[0].ID)
	if err == nil || !l.Pending || l.CheckNote == "" {
		t.Fatalf("after decline: link = %+v, err = %v; want it still pending with a note", l, err)
	}
}

func TestCuttingOffAConnectionRevokesEveryVHost(t *testing.T) {
	ctx := context.Background()
	p := newPair(t)
	first := p.connect(t, true)
	added, err := p.stg.AddVHosts(ctx, first.ID, []VHostRoute{{VHost: "billing", TargetVHost: "billing"}, {VHost: "hr", TargetVHost: "hr"}}, "gembit")
	if err != nil || len(added) != 2 {
		t.Fatalf("added = %+v, %v", added, err)
	}
	root := mustPeer(t, p, first)
	if err := p.prd.RevokePeerGroup(ctx, root.ID); err != nil {
		t.Fatal(err)
	}
	if peers, _ := p.prd.Peers(ctx); len(peers) != 0 {
		t.Fatalf("peers left: %+v", peers)
	}

	// Removing one vhost on the sending side leaves the others working.
	p2 := newPair(t)
	f2 := p2.connect(t, true)
	a2, _ := p2.stg.AddVHosts(ctx, f2.ID, []VHostRoute{{VHost: "billing", TargetVHost: "billing"}}, "gembit")
	if _, err := p2.stg.Unlink(ctx, a2[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := p2.stg.TestLink(ctx, f2.ID); err != nil {
		t.Fatalf("the first vhost stopped working after another was removed: %v", err)
	}
	if peers, _ := p2.prd.Peers(ctx); len(peers) != 1 || peers[0].VHost != "orders" {
		t.Fatalf("peers = %+v", peers)
	}
}

func TestAnAddedVHostCannotWidenAnotherPeersReach(t *testing.T) {
	ctx := context.Background()
	p := newPair(t)
	first := p.connect(t, true)
	// A peer from a different connection does not join this one's group.
	code, _, _ := p.prd.NewPairingCode(ctx, "", "hr", "rina")
	other, err := p.stg.LinkWith(ctx, LinkRequest{URL: "https://prod.example.com", Code: code, SelfURL: "https://stg.example.com", VHost: "hr"}, "gembit")
	if err != nil {
		t.Fatal(err)
	}
	// The hr connection was paired without permission to add vhosts.
	added, err := p.stg.AddVHosts(ctx, other.ID, []VHostRoute{{VHost: "billing", TargetVHost: "billing"}}, "gembit")
	if err != nil || !added[0].Pending {
		t.Fatalf("added = %+v, %v; the hr connection may not add vhosts on its own", added, err)
	}
	_ = first
}
