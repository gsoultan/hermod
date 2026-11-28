// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package advanced

import (
	"errors"
	"strings"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/infra/dbactivity"
)

// --- Execute SQL steps could take every connection lookups needed ---------
//
// Reported on a database whose PgBouncer gave the hermod user 12 server
// connections. Long statements filled all 12, and every DB Lookup behind them
// timed out. An Execute SQL step now takes one of the bulk slots the source's
// database allows (three quarters of Max connections by default), so the rest
// stay free for lookups.

func TestExecuteSQLWaitsForABulkSlotAndNeverRunsWithoutOne(t *testing.T) {
	tr, reg := newExecSQLFixture(t)
	d := dbactivity.For("test|" + t.Name())
	d.SetLimit(1)
	dbactivity.Bind(reg.db, d)
	t.Cleanup(func() { dbactivity.Unbind(reg.db) })

	held, err := d.Begin(t.Context(), dbactivity.Query{Kind: dbactivity.SourcePoll, WorkflowID: "report"})
	if err != nil {
		t.Fatal(err)
	}

	_, err = runExecSQL(t, tr, reg, map[string]any{
		"sourceId": "src1", "queryTemplate": "INSERT INTO audit (id, note) VALUES ('1', 'x')", "timeout": "100ms",
	}, nil)
	if err == nil {
		t.Fatal("a step with no free slot ran anyway")
	}
	if !strings.Contains(err.Error(), "kept free for lookups") {
		t.Errorf("error does not say why the step waited:\n%v", err)
	}
	// It never reached the database, so it is safe to wait out and retry.
	if !errors.Is(err, hermod.ErrBusy) {
		t.Errorf("a step that never ran is not marked busy: %v", err)
	}
	if n := countAudit(t, reg); n != 0 {
		t.Fatalf("the statement ran without a slot: %d rows", n)
	}

	held()
	if _, err := runExecSQL(t, tr, reg, map[string]any{
		"sourceId": "src1", "queryTemplate": "INSERT INTO audit (id, note) VALUES ('1', 'x')", "timeout": "1s",
	}, nil); err != nil {
		t.Fatalf("the step failed once the slot was free: %v", err)
	}
	if _, bulk := d.Running(); bulk != 0 {
		t.Errorf("the step kept its slot after finishing: %d still held", bulk)
	}
}

// A step that waited for a bulk slot waited behind whoever held the slots, and
// the Runs view shows them, so the busy answer names them as data. Every
// holder counts, however young: it is a slot, not a slow query, that the step
// waited for.
func TestExecuteSQLBusyAnswerNamesTheSlotHolders(t *testing.T) {
	tr, reg := newExecSQLFixture(t)
	d := dbactivity.For("test|" + t.Name())
	d.SetLimit(1)
	dbactivity.Bind(reg.db, d)
	t.Cleanup(func() { dbactivity.Unbind(reg.db) })

	held, err := d.Begin(t.Context(), dbactivity.Query{Kind: dbactivity.SourcePoll, WorkflowID: "report", NodeID: "src"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(held)

	_, err = runExecSQL(t, tr, reg, map[string]any{
		"sourceId": "src1", "queryTemplate": "INSERT INTO audit (id, note) VALUES ('1', 'x')", "timeout": "50ms",
	}, nil)
	var busy *hermod.Busy
	if !errors.As(err, &busy) {
		t.Fatalf("err = %v, want a *hermod.Busy", err)
	}
	if len(busy.Holders) != 1 || busy.Holders[0].WorkflowID != "report" || busy.Holders[0].Kind != "source poll" {
		t.Errorf("Holders = %+v, want report's source poll", busy.Holders)
	}
}
