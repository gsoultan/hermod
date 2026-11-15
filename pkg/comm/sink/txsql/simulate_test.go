// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package txsql

import (
	"strings"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/infra/sqlutil"
)

func TestSimulateReportsEveryStatementAndLeavesNoRowBehind(t *testing.T) {
	snk, db := newSQLiteSink(t, orderStatements, nil)
	msg := newMsg(t, "m1", order("E-1", map[string]any{"sku": "A", "qty": 2}, map[string]any{"sku": "B", "qty": 1}))

	rep, err := snk.Simulate(t.Context(), msg)
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if rep.Error != "" {
		t.Fatalf("report error: %s", rep.Error)
	}
	// order once, items twice, audit once.
	if got := len(rep.Steps); got != 4 {
		t.Fatalf("steps = %d, want 4: %+v", got, rep.Steps)
	}
	first := rep.Steps[0]
	if first.Name != "order" || len(first.Rows) != 1 || first.Rows[0]["id"] == nil {
		t.Fatalf("order step = %+v, want one returned row with an id", first)
	}
	if !strings.Contains(first.SQL, "?") || strings.Contains(first.SQL, "{{") {
		t.Fatalf("order SQL = %q, want placeholders, not tokens", first.SQL)
	}
	if len(first.Args) != 3 || first.Args[0] != "E-1" {
		t.Fatalf("order args = %v", first.Args)
	}
	if rep.Steps[1].Name != "items" || rep.Steps[1].Item != 1 || rep.Steps[2].Item != 2 {
		t.Fatalf("item steps = %+v %+v, want items 1 and 2", rep.Steps[1], rep.Steps[2])
	}
	if rep.Steps[3].Name != "audit" || rep.Steps[3].Affected != 1 {
		t.Fatalf("audit step = %+v, want 1 row changed", rep.Steps[3])
	}
	if !rep.RolledBack {
		t.Fatal("report does not say the work was rolled back")
	}
	for _, q := range []string{"SELECT count(*) FROM orders", "SELECT count(*) FROM order_items", "SELECT count(*) FROM customers WHERE last_order_id IS NOT NULL"} {
		if n := count(t, db, q); n != 0 {
			t.Fatalf("%s = %d after a simulation, want 0", q, n)
		}
	}
}

func TestSimulateStopsAtTheFailingStatementAndSaysWhy(t *testing.T) {
	snk, db := newSQLiteSink(t, orderStatements, nil)
	// The audit UPDATE matches no customer, so expect "affected" fails it.
	fields := order("E-2", map[string]any{"sku": "A", "qty": 2})
	fields["customer"] = "nobody"
	msg := newMsg(t, "m2", fields)

	rep, err := snk.Simulate(t.Context(), msg)
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if rep.Error == "" || !strings.Contains(rep.Error, "audit") {
		t.Fatalf("report error = %q, want it to name the audit statement", rep.Error)
	}
	last := rep.Steps[len(rep.Steps)-1]
	if last.Name != "audit" || last.Error == "" {
		t.Fatalf("last step = %+v, want the failed audit", last)
	}
	if n := count(t, db, "SELECT count(*) FROM orders"); n != 0 {
		t.Fatalf("orders = %d after a failed simulation, want 0", n)
	}
}

func TestSimulateInsideARunSandboxSeesEarlierWorkAndCommitsNothing(t *testing.T) {
	snk, db := newSQLiteSink(t, `[{"name":"bump","sql":"UPDATE customers SET last_order_id = 7 WHERE id = {{customer}}","expect":"one"}]`, nil)
	sb := sqlutil.NewSandbox(t.Context())
	ctx := sqlutil.WithSandbox(t.Context(), sb)
	// An earlier node of the same run inserted this customer.
	q, err := sqlutil.Use(ctx, db, "sqlite")
	if err != nil {
		t.Fatalf("Use: %v", err)
	}
	if _, err := q.ExecContext(ctx, "INSERT INTO customers (id) VALUES ('C-NEW')"); err != nil {
		t.Fatalf("earlier node: %v", err)
	}

	rep, err := snk.Simulate(ctx, newMsg(t, "m3", map[string]any{"customer": "C-NEW"}))
	if err != nil || rep.Error != "" {
		t.Fatalf("Simulate: %v / %s", err, rep.Error)
	}
	if err := sb.Rollback(); err != nil {
		t.Fatalf("sandbox rollback: %v", err)
	}
	if n := count(t, db, "SELECT count(*) FROM customers WHERE id = 'C-NEW'"); n != 0 {
		t.Fatalf("the run's customer survived its rollback")
	}
}

func TestSimulateCapsTheRowsItReports(t *testing.T) {
	snk, _ := newSQLiteSink(t, `[{"name":"many","sql":"WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i < 50) SELECT i FROM n","expect":"any"}]`, nil)
	rep, err := snk.Simulate(t.Context(), newMsg(t, "m4", nil))
	if err != nil || rep.Error != "" {
		t.Fatalf("Simulate: %v / %s", err, rep.Error)
	}
	st := rep.Steps[0]
	if len(st.Rows) != hermod.MaxSimulatedRows || st.RowCount != 50 {
		t.Fatalf("rows kept = %d, counted = %d; want %d and 50", len(st.Rows), st.RowCount, hermod.MaxSimulatedRows)
	}
}

var _ hermod.SimulatingSink = (*Sink)(nil)
