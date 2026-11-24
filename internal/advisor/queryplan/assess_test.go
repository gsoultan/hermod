// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package queryplan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func plan(t *testing.T, name string) []Scan {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	scans, err := ParsePlan(raw)
	if err != nil {
		t.Fatalf("ParsePlan(%s): %v", name, err)
	}
	return scans
}

// catalog is the fixture database's tables, as Catalog would read them.
func catalog() map[string]Table {
	return map[string]Table{
		"etl.SODTL": {Schema: "etl", Name: "SODTL", Rows: 50000, Indexes: []Index{
			{Name: "sodtl_docno", Method: "btree", Keys: []string{`"DocNo"`}},
			{Name: "sodtl_state_hash", Method: "hash", Keys: []string{`"State"`}},
		}},
		"public.sales_orders": {Schema: "public", Name: "sales_orders", Rows: 40000, Indexes: []Index{
			{Name: "so_doc", Method: "btree", Keys: []string{"doc_no"}},
		}},
	}
}

func only(t *testing.T, fs []Finding, rule string) Finding {
	t.Helper()
	var got []Finding
	for _, f := range fs {
		if f.Rule == rule {
			got = append(got, f)
		}
	}
	if len(got) != 1 {
		t.Fatalf("want one %q finding, got %d in %+v", rule, len(got), fs)
	}
	return got[0]
}

func TestParsePlanReadsScansAndJoinConditions(t *testing.T) {
	scans := plan(t, "funcjoin")
	if len(scans) != 3 {
		t.Fatalf("want 3 scans (bitmap heap, bitmap index, seq), got %+v", scans)
	}
	var seq Scan
	for _, s := range scans {
		if s.Node == "Seq Scan" {
			seq = s
		}
	}
	if seq.Schema != "public" || seq.Relation != "sales_orders" || seq.Alias != "s" {
		t.Fatalf("seq scan = %+v", seq)
	}
	if len(seq.Conds) != 1 || !strings.Contains(seq.Conds[0], "TRIM(BOTH FROM lower(s.doc_no))") {
		t.Fatalf("the join condition must reach the scan it filters: %q", seq.Conds)
	}
}

func TestIndexUsedIsReportedWithItsType(t *testing.T) {
	f := only(t, Assess(plan(t, "indexed"), catalog()), RuleIndexUsed)
	if f.Severity != SeverityOK || !strings.Contains(f.Title, "so_doc") || !strings.Contains(f.Detail, "btree") {
		t.Fatalf("finding = %+v", f)
	}
	if f.Proposal != "" {
		t.Fatalf("nothing to propose for a good index, got %q", f.Proposal)
	}
}

func TestFunctionWrappedJoinKeyProposesExpressionIndex(t *testing.T) {
	f := only(t, Assess(plan(t, "funcjoin"), catalog()), RuleWrappedColumn)
	if f.Severity != SeverityWarn || f.Table != "public.sales_orders" {
		t.Fatalf("finding = %+v", f)
	}
	if !strings.Contains(f.Detail, "so_doc") {
		t.Fatalf("detail should name the index the wrap defeats: %q", f.Detail)
	}
	want := `CREATE INDEX CONCURRENTLY IF NOT EXISTS sales_orders_trim_lower_doc_no_idx ON public.sales_orders ((TRIM(BOTH FROM lower(doc_no))));`
	if f.Proposal != want {
		t.Fatalf("proposal\n got %s\nwant %s", f.Proposal, want)
	}
}

func TestFunctionWrappedFilterProposesExpressionIndex(t *testing.T) {
	f := only(t, Assess(plan(t, "funcfilter"), catalog()), RuleWrappedColumn)
	if !strings.Contains(f.Proposal, "((lower(doc_no)))") {
		t.Fatalf("proposal = %q", f.Proposal)
	}
}

func TestExistingExpressionIndexIsNotProposedAgain(t *testing.T) {
	c := catalog()
	so := c["public.sales_orders"]
	so.Indexes = append(so.Indexes, Index{Name: "so_lower", Method: "btree", Keys: []string{"lower(doc_no)"}})
	c["public.sales_orders"] = so
	for _, f := range Assess(plan(t, "funcfilter"), c) {
		if f.Proposal != "" {
			t.Fatalf("an index on the expression already exists, got %+v", f)
		}
	}
}

func TestLeadingWildcardLikeProposesTrigramIndex(t *testing.T) {
	f := only(t, Assess(plan(t, "likewild"), catalog()), RuleLeadingWildcard)
	if !strings.Contains(f.Proposal, "CREATE EXTENSION IF NOT EXISTS pg_trgm;") ||
		!strings.Contains(f.Proposal, "USING gin (doc_no gin_trgm_ops)") {
		t.Fatalf("proposal = %q", f.Proposal)
	}
}

func TestRangeOnHashOnlyColumnProposesBtree(t *testing.T) {
	f := only(t, Assess(plan(t, "hashrange"), catalog()), RuleWrongIndexType)
	if !strings.Contains(f.Detail, "sodtl_state_hash") || !strings.Contains(f.Proposal, `ON etl."SODTL" ("State")`) {
		t.Fatalf("finding = %+v", f)
	}
}

func TestJSONBContainmentProposesGIN(t *testing.T) {
	f := only(t, Assess(plan(t, "jsonb"), catalog()), RuleJSONB)
	if !strings.Contains(f.Proposal, "USING gin (meta jsonb_path_ops)") {
		t.Fatalf("proposal = %q", f.Proposal)
	}
}

func TestUnindexedFilterProposesCompositeEqualityFirst(t *testing.T) {
	f := only(t, Assess(plan(t, "seqeq"), catalog()), RuleMissingIndex)
	if !strings.Contains(f.Proposal, `ON etl."SODTL" ("Branch", "LastUpdated")`) {
		t.Fatalf("equality columns go first, range last: %q", f.Proposal)
	}
}

func TestSmallTableFullScanIsFine(t *testing.T) {
	c := catalog()
	so := c["public.sales_orders"]
	so.Rows = 200
	c["public.sales_orders"] = so
	f := only(t, Assess(plan(t, "likewild"), c), RuleSmallTable)
	if f.Severity != SeverityOK || f.Proposal != "" {
		t.Fatalf("finding = %+v", f)
	}
}

func TestSplitPredicatesRespectsParensAndQuotes(t *testing.T) {
	got := splitAnd(`((a."x" = 'b AND c') AND (lower(a.y) = $1))`)
	if len(got) != 2 || got[0] != `a."x" = 'b AND c'` || got[1] != `lower(a.y) = $1` {
		t.Fatalf("splitAnd = %q", got)
	}
}

func TestNeverAnalyzedTableIsNotCalledSmall(t *testing.T) {
	c := catalog()
	so := c["public.sales_orders"]
	so.Rows = -1
	c["public.sales_orders"] = so
	f := only(t, Assess(plan(t, "likewild"), c), RuleLeadingWildcard)
	if strings.Contains(f.Detail, "-1") {
		t.Fatalf("an unknown size must not be printed: %q", f.Detail)
	}
	for _, f := range Assess(plan(t, "likewild"), c) {
		if f.Rule == RuleSmallTable {
			t.Fatalf("a table never analyzed has no known size: %+v", f)
		}
	}
}
