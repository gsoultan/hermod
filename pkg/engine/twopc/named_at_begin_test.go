// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package twopc

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

// Some participants have to name their transaction when they open it, not
// when they prepare it. MySQL XA is one: XA START takes the identifier, and an
// XA transaction cannot be renamed afterwards. A coordinator that only revealed
// the name at Prepare would force such a participant to invent its own at
// Begin and report it back from Prepare — reopening exactly the window the
// coordinator-supplied ID closes, because a crash between that Prepare
// returning and the log recording the substitute leaves a prepared transaction
// nothing can name.
//
// So the coordinator chooses each participant's ID before Begin and hands it
// over through hermod.NamedBegin. The ordering property that matters is
// unchanged and still asserted by TestTheTransactionIsNamedBeforeItCanExist:
// the name is durable before Prepare. An *open* transaction that has not been
// prepared is not in doubt — a crash rolls it back — so naming it before the
// log has it costs nothing.

// namingParticipant opens its transaction under a supplied name and refuses to
// prepare under any other, as the MySQL sink does.
type namingParticipant struct {
	fakeParticipant

	mu2         sync.Mutex
	plainBegins int
	beganWith   string
	preparedAs  string
}

func (p *namingParticipant) Begin(context.Context) error {
	p.mu2.Lock()
	defer p.mu2.Unlock()
	p.plainBegins++
	return nil
}

func (p *namingParticipant) BeginNamed(_ context.Context, txID string) error {
	p.mu2.Lock()
	defer p.mu2.Unlock()
	if txID == "" {
		return errors.New("BeginNamed called without a transaction ID")
	}
	p.beganWith = txID
	return nil
}

func (p *namingParticipant) Prepare(ctx context.Context, txID string) (string, error) {
	p.mu2.Lock()
	began := p.beganWith
	p.preparedAs = txID
	p.mu2.Unlock()
	if began != txID {
		return "", errors.New("asked to prepare under " + txID + " but began under " + began)
	}
	return p.fakeParticipant.Prepare(ctx, txID)
}

func TestAParticipantThatNamesAtBeginIsGivenTheIDItWillPrepareUnder(t *testing.T) {
	store := newMemStore()
	c := newTestCoordinator(t, store)

	named := &namingParticipant{fakeParticipant: fakeParticipant{name: "mysql"}}
	plain := &fakeParticipant{name: "postgres"}

	if err := c.Run(context.Background(),
		[]Participant{{ID: "mysql", Sink: named}, {ID: "postgres", Sink: plain}},
		func(context.Context) error { return nil }); err != nil {
		t.Fatalf("Run: %v", err)
	}

	named.mu2.Lock()
	defer named.mu2.Unlock()
	if named.beganWith == "" {
		t.Fatalf("the coordinator never called BeginNamed (plain Begin called %d time(s)); "+
			"a participant that must name its transaction at start would have to invent "+
			"the name itself, and a crash before the log learnt it would orphan a prepared transaction",
			named.plainBegins)
	}
	if named.plainBegins != 0 {
		t.Errorf("Begin was called %d time(s) as well as BeginNamed; the participant would open two transactions",
			named.plainBegins)
	}
	if named.preparedAs != named.beganWith {
		t.Errorf("began under %q, asked to prepare under %q", named.beganWith, named.preparedAs)
	}
	if plain.lastTxID == "" || plain.lastTxID == named.beganWith {
		t.Errorf("participants must get distinct IDs: mysql=%q postgres=%q", named.beganWith, plain.lastTxID)
	}
	if n := len(store.kv); n != 0 {
		t.Errorf("a committed transaction left %d record(s) in the log", n)
	}
}

// A participant that fails to open its named transaction aborts the round
// before anything is written, and the ones already opened are rolled back.
func TestAFailedNamedBeginRollsBackTheOthers(t *testing.T) {
	store := newMemStore()
	c := newTestCoordinator(t, store)

	first := &fakeParticipant{name: "postgres"}
	failing := &failingNamedBegin{}

	wrote := false
	err := c.Run(context.Background(),
		[]Participant{{ID: "postgres", Sink: first}, {ID: "mysql", Sink: failing}},
		func(context.Context) error { wrote = true; return nil })
	if err == nil || !strings.Contains(err.Error(), "could not begin") {
		t.Fatalf("Run = %v, want a begin failure", err)
	}
	if wrote {
		t.Error("work ran although a participant could not open its transaction")
	}
	if first.rolledBack != 1 {
		t.Errorf("the participant already opened was rolled back %d time(s), want 1", first.rolledBack)
	}
	if first.prepared != 0 {
		t.Errorf("something was prepared (%d) after a begin failure", first.prepared)
	}
}

type failingNamedBegin struct{ fakeParticipant }

func (p *failingNamedBegin) BeginNamed(context.Context, string) error {
	return errors.New("XA START refused")
}
