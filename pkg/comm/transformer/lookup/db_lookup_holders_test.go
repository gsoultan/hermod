// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package lookup

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/infra/dbactivity"
)

// --- "the wait was in the database or PgBouncer" did not say who held it ---
//
// Reported on a database whose PgBouncer gave the hermod user 12 server
// connections: all 12 were running Hermod's own reporting queries, some for
// two hours, and every lookup behind them timed out. The error said the wait
// was in PgBouncer, which was true and useless: the operator still had to go
// and find which workflows were holding the connections. Hermod knows, because
// it started those queries, so the error now names them.

func TestDBLookupTimeoutNamesTheWorkflowsHoldingTheDatabase(t *testing.T) {
	reg, _ := hangingLookupRegistry(t)
	reg.db.SetMaxOpenConns(5)

	d := dbactivity.For("test|" + t.Name())
	dbactivity.Bind(reg.db, d)
	t.Cleanup(func() { dbactivity.Unbind(reg.db) })

	end, err := d.Begin(t.Context(), dbactivity.Query{
		Kind: dbactivity.SourcePoll, WorkflowID: "nightly-report", Started: time.Now().Add(-2 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(end)
	endShort, _ := d.Begin(t.Context(), dbactivity.Query{Kind: dbactivity.SQLStep, WorkflowID: "quick-step"})
	t.Cleanup(endShort)

	err = runHungLookup(lookupCtx(t.Context(), reg), t, tableLookupConfig(map[string]any{"timeout": "100ms"}))
	if err == nil {
		t.Fatal("a query that never answered succeeded")
	}
	msg := err.Error()
	if !strings.Contains(msg, `workflow "nightly-report" (source poll, running 2h0m`) {
		t.Errorf("error does not name the workflow holding the database:\n%v", msg)
	}
	if strings.Contains(msg, "quick-step") {
		t.Errorf("error names a query that has only just started, which is not what the lookup waited behind:\n%v", msg)
	}
	if all, _ := d.Running(); all != 2 {
		t.Errorf("the lookup is still recorded as running after it gave up: %d queries running, want the 2 held", all)
	}
	// It waited for a connection behind Hermod's own queries, and a lookup is a
	// read: the engine waits it out instead of failing the message.
	if !errors.Is(err, hermod.ErrBusy) {
		t.Errorf("a lookup that queued behind Hermod's own queries is not marked busy: %v", err)
	}
	if !strings.Contains(msg, "Max connections") {
		t.Errorf("error does not say what to change:\n%v", msg)
	}
}

// A lookup that timed out while the tuner saw the database with no connection
// free waited for a connection, not for its query, so it is busy too.
func TestDBLookupTimeoutWhileTheDatabaseQueuedIsBusy(t *testing.T) {
	reg, _ := hangingLookupRegistry(t)
	reg.db.SetMaxOpenConns(5)
	d := dbactivity.For("test|" + t.Name())
	dbactivity.Bind(reg.db, d)
	t.Cleanup(func() { dbactivity.Unbind(reg.db) })

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			case <-time.After(10 * time.Millisecond):
				d.MarkQueued(time.Now())
			}
		}
	}()
	t.Cleanup(func() { close(stop); <-done })

	err := runHungLookup(lookupCtx(t.Context(), reg), t, tableLookupConfig(map[string]any{"timeout": "100ms"}))
	if !errors.Is(err, hermod.ErrBusy) {
		t.Fatalf("a lookup that timed out while the database had no connection free is not busy: %v", err)
	}
}

// And one that timed out with nothing to show it queued stays an error: the
// query itself may be what is slow, and waiting would hide that.
func TestDBLookupTimeoutWithNoSignOfQueueingIsNotBusy(t *testing.T) {
	reg, _ := hangingLookupRegistry(t)
	reg.db.SetMaxOpenConns(5)
	d := dbactivity.For("test|" + t.Name())
	dbactivity.Bind(reg.db, d)
	t.Cleanup(func() { dbactivity.Unbind(reg.db) })

	err := runHungLookup(lookupCtx(t.Context(), reg), t, tableLookupConfig(map[string]any{"timeout": "100ms"}))
	if err == nil || errors.Is(err, hermod.ErrBusy) {
		t.Fatalf("a lookup whose own query was slow was marked busy: %v", err)
	}
}
