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
	"strings"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
)

// The query-plan check plans the statement a running lookup sends. These pin
// that LookupStatement builds the same text the lookup runs, with every value
// that comes from a message left as a placeholder.
func TestLookupStatementQueryModeLeavesEveryTokenAPlaceholder(t *testing.T) {
	st, err := LookupStatement(map[string]any{
		"mode":          "query",
		"queryTemplate": "SELECT * FROM etl.\"SODTL\" WHERE \"DocNo\" = {{after.doc}} AND \"LastUpdated\" >= now() - {{filter.delete}}::INTERVAL",
	}, "pgx")
	if err != nil {
		t.Fatal(err)
	}
	want := "SELECT * FROM etl.\"SODTL\" WHERE \"DocNo\" = $1 AND \"LastUpdated\" >= now() - $2::INTERVAL"
	if st.SQL != want || st.Params != 2 {
		t.Fatalf("got %q with %d params, want %q with 2", st.SQL, st.Params, want)
	}
}

func TestLookupStatementTableModeIsTheStatementTheLookupRuns(t *testing.T) {
	st, err := LookupStatement(map[string]any{
		"table": "public.orders", "keyColumn": "doc_no", "valueColumn": "status",
		"whereClause": "office = 'JKT' AND state = {{after.state}}",
	}, "pgx")
	if err != nil {
		t.Fatal(err)
	}
	// The same builder lookupSQL calls, so the two cannot drift.
	want, _, _, err := tableLookupQuery("pgx", "public.orders", "doc_no", nil, "office = 'JKT' AND state = {{after.state}}", "status", mapClause(nil))
	if err != nil {
		t.Fatal(err)
	}
	if st.SQL != want {
		t.Fatalf("got %q, want %q", st.SQL, want)
	}
	if st.Params != strings.Count(want, "$") {
		t.Errorf("params = %d for %q", st.Params, want)
	}
}

func TestLookupStatementRefusesAnIncompleteNode(t *testing.T) {
	for name, cfg := range map[string]map[string]any{
		"no table":    {"keyColumn": "id"},
		"empty query": {"mode": "query", "queryTemplate": "  "},
	} {
		if _, err := LookupStatement(cfg, "pgx"); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestLookupTargetResolvesTheSourceTheNodeQueries(t *testing.T) {
	reg := &dialectFakeRegistry{sources: map[string]storage.Source{
		"pg1": {ID: "pg1", Name: "ETL", Type: "postgres", Config: hermod.StringMap{"use_cdc": "false"}},
		"b1":  {ID: "b1", Type: "batch_sql", Config: hermod.StringMap{"source_id": "pg1", "use_cdc": "false"}},
		"cdc": {ID: "cdc", Name: "Live", Type: "postgres", Config: hermod.StringMap{"use_cdc": "true"}},
	}}
	src, driver, err := LookupTarget(t.Context(), reg, map[string]any{"sourceId": "b1"})
	if err != nil || src.ID != "b1" || driver != "pgx" {
		t.Fatalf("batch_sql over postgres: src=%s driver=%q err=%v", src.ID, driver, err)
	}
	if _, _, err := LookupTarget(t.Context(), reg, map[string]any{"sourceId": "cdc"}); err == nil ||
		!strings.Contains(err.Error(), "non-CDC") {
		t.Fatalf("a CDC source is refused as the node refuses it, got %v", err)
	}
	if _, _, err := LookupTarget(t.Context(), reg, map[string]any{}); err == nil {
		t.Fatal("a node with no source must say so")
	}
}
