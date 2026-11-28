// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

//go:build integration

package queryplan

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// Run against a real PostgreSQL:
//
//	POSTGRES_DSN='postgres://postgres@127.0.0.1:5432/postgres?sslmode=disable' \
//	  go test -tags integration -run TestRun ./internal/advisor/queryplan/
func openPG(t *testing.T) (*sql.DB, string) {
	t.Helper()
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not set")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1) // every call shares one session, so leaks show
	t.Cleanup(func() { _ = db.Close() })
	ctx := t.Context()
	schema := fmt.Sprintf("hermod_qp_%d", time.Now().UnixNano())
	for _, q := range []string{
		"CREATE SCHEMA " + schema,
		"CREATE TABLE " + schema + ".orders (id int, doc_no text)",
		"INSERT INTO " + schema + ".orders SELECT g, 'SO-' || g FROM generate_series(1, 30000) g",
		"CREATE INDEX orders_doc ON " + schema + ".orders (doc_no)",
		"ANALYZE " + schema + ".orders",
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.WithoutCancel(ctx), "DROP SCHEMA "+schema+" CASCADE")
	})
	return db, schema
}

func TestRunFindsWrappedKeyAndProposesIndex(t *testing.T) {
	db, schema := openPG(t)
	got, err := Run(t.Context(), db, "SELECT id FROM "+schema+".orders WHERE TRIM(LOWER(doc_no)) = $1")
	if err != nil {
		t.Fatal(err)
	}
	var wrapped *Finding
	for i, f := range got.Findings {
		if f.Rule == RuleWrappedColumn {
			wrapped = &got.Findings[i]
		}
	}
	if wrapped == nil || !strings.Contains(wrapped.Detail, "orders_doc") ||
		!strings.Contains(wrapped.Proposal, "((TRIM(BOTH FROM lower(doc_no))))") {
		t.Fatalf("findings = %+v", got.Findings)
	}
	if len(got.Tables) != 1 || got.Tables[0].Rows < 29000 || got.Tables[0].Indexes[0].Method != "btree" {
		t.Fatalf("tables = %+v", got.Tables)
	}
}

func TestRunReportsIndexInUse(t *testing.T) {
	db, schema := openPG(t)
	got, err := Run(t.Context(), db, "SELECT id FROM "+schema+".orders WHERE doc_no = $1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Findings) != 1 || got.Findings[0].Rule != RuleIndexUsed || !strings.Contains(got.Findings[0].Title, "orders_doc") {
		t.Fatalf("findings = %+v", got.Findings)
	}
}

func TestRunNeverExecutesTheQuery(t *testing.T) {
	db, schema := openPG(t)
	if _, err := Run(t.Context(), db, "DELETE FROM "+schema+".orders WHERE doc_no = $1 OR true"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM "+schema+".orders").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 30000 {
		t.Fatalf("planning deleted rows: %d left", n)
	}
}

func TestRunRefusesASecondStatement(t *testing.T) {
	db, schema := openPG(t)
	_, err := Run(t.Context(), db, "SELECT 1; DROP TABLE "+schema+".orders")
	if err == nil {
		t.Fatal("a query with two statements must be refused")
	}
	var n int
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM "+schema+".orders").Scan(&n); err != nil {
		t.Fatalf("the table must still exist: %v", err)
	}
}

func TestRunLeavesNoPreparedStatementOrTransaction(t *testing.T) {
	db, schema := openPG(t)
	for range 3 {
		if _, err := Run(t.Context(), db, "SELECT id FROM "+schema+".orders WHERE doc_no = $1"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Run(t.Context(), db, "SELECT nope FROM "+schema+".orders"); err == nil {
		t.Fatal("a bad query must report its error")
	}
	var n int
	if err := db.QueryRowContext(t.Context(),
		"SELECT count(*) FROM pg_prepared_statements WHERE name LIKE 'hermod_plan%'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d plan-check statements left on the session", n)
	}
	// A write proves the session is not left inside the read-only transaction.
	if _, err := db.ExecContext(t.Context(), "INSERT INTO "+schema+".orders VALUES (0, 'x')"); err != nil {
		t.Fatalf("session left read-only: %v", err)
	}
}

func TestCheckLookupsPlansEachNodeAsItRuns(t *testing.T) {
	db, schema := openPG(t)
	srcs := &fakeSources{db: db, sources: map[string]storage.Source{
		"etl": {ID: "etl", Name: "ETL", Type: "postgres", Config: hermod.StringMap{"use_cdc": "false"}},
	}}
	wf := storage.Workflow{Nodes: []storage.WorkflowNode{
		lookupNode("good", "By doc", "etl", "SELECT id FROM "+schema+".orders WHERE doc_no = {{after.doc}}"),
		lookupNode("slow", "By cleaned doc", "etl", "SELECT id FROM "+schema+".orders WHERE TRIM(LOWER(doc_no)) = TRIM(LOWER({{after.doc}}))"),
		{ID: "tbl", Type: "transformation", Config: map[string]any{"transType": "db_lookup", "label": "Table mode",
			"sourceId": "etl", "mode": "table", "table": schema + ".orders", "keyColumn": "doc_no", "valueColumn": "id"}},
	}}
	got := CheckLookups(t.Context(), wf, srcs)
	if len(got) != 3 {
		t.Fatalf("got %+v", got)
	}
	for _, c := range got {
		if c.Error != "" {
			t.Fatalf("%s: %s (sql %s)", c.NodeID, c.Error, c.SQL)
		}
	}
	if got[0].Findings[0].Rule != RuleIndexUsed || got[2].Findings[0].Rule != RuleIndexUsed {
		t.Fatalf("query and table mode on the indexed column use the index: %+v / %+v", got[0].Findings, got[2].Findings)
	}
	if got[1].Findings[0].Rule != RuleWrappedColumn || !strings.Contains(got[1].SQL, "$1") {
		t.Fatalf("the wrapped lookup: %+v", got[1])
	}
}

// A day filter written as date_trunc on the column reads the whole table,
// although an index on the column exists. The finding offers the range that
// selects the same rows, and the rewritten query really does use the index.
func TestRunOffersARangeForADateTruncFilter(t *testing.T) {
	db, schema := openPG(t)
	for _, q := range []string{
		"CREATE TABLE " + schema + ".sales (id int, created_at timestamp)",
		"INSERT INTO " + schema + ".sales SELECT g, now() - g * interval '1 minute' FROM generate_series(1, 30000) g",
		"CREATE INDEX sales_created ON " + schema + ".sales (created_at)",
		"ANALYZE " + schema + ".sales",
	} {
		if _, err := db.ExecContext(t.Context(), q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	query := "SELECT id FROM " + schema + ".sales s WHERE date_trunc('day', s.created_at) = $1"
	got, err := Run(t.Context(), db, query)
	if err != nil {
		t.Fatal(err)
	}
	var f *Finding
	for i := range got.Findings {
		if got.Findings[i].Rewrite != "" {
			f = &got.Findings[i]
		}
	}
	if f == nil || !strings.Contains(f.Detail, "sales_created") {
		t.Fatalf("findings = %+v", got.Findings)
	}
	// The query and the condition alike, rewritten with the column's type
	// from the catalog, plan, and read the index.
	better, ok := RewriteTruncEquality(query, got.Tables[0].ColumnType)
	if !ok {
		t.Fatal("the query was not rewritten")
	}
	for _, q := range []string{better, "SELECT id FROM " + schema + ".sales s WHERE " + f.Rewrite} {
		again, err := Run(t.Context(), db, q)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		if !slices.ContainsFunc(again.Scans, func(s Scan) bool { return s.Index == "sales_created" }) {
			t.Errorf("%q does not use the index: %+v", q, again.Scans)
		}
	}
}
