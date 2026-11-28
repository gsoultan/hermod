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

import "testing"

func TestRewriteTruncEquality(t *testing.T) {
	for _, tc := range []struct {
		name, in, want string
	}{
		{
			name: "a filter on yesterday",
			in:   "SELECT * FROM sales_line l WHERE date_trunc('day', l.created_at) = CURRENT_DATE - 1 ORDER BY l.id",
			want: "SELECT * FROM sales_line l WHERE (l.created_at >= (CURRENT_DATE - 1) AND l.created_at < (CURRENT_DATE - 1) + interval '1 day') ORDER BY l.id",
		},
		{
			name: "a message value, kept as the template it is",
			in:   "SELECT id FROM sl WHERE DATE_TRUNC('month', created_at) = {{.after.month}} AND amount > 0",
			want: "SELECT id FROM sl WHERE (created_at >= ({{.after.month}}) AND created_at < CAST({{.after.month}} AS timestamp without time zone) + interval '1 month') AND amount > 0",
		},
		{
			// The replacement is parenthesised: NOT, or an OR beside it, must
			// still apply to the whole condition.
			name: "under NOT",
			in:   "SELECT 1 FROM sl WHERE NOT date_trunc('hour', created_at) = $1",
			want: "SELECT 1 FROM sl WHERE NOT (created_at >= ($1) AND created_at < CAST($1 AS timestamp without time zone) + interval '1 hour')",
		},
		{
			name: "inside parentheses and beside a function call",
			in:   "SELECT 1 FROM sl WHERE (date_trunc('day', created_at) = date(now()) OR id = 1)",
			want: "SELECT 1 FROM sl WHERE ((created_at >= (date(now())) AND created_at < (date(now())) + interval '1 day') OR id = 1)",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := RewriteTruncEquality(tc.in, func(string) string { return "timestamp without time zone" })
			if !ok || got != tc.want {
				t.Errorf("got %q, %v\nwant %q", got, ok, tc.want)
			}
		})
	}
}

// Only what means the same rows is rewritten. A range comparison on the
// truncated value, or an expression rather than a column, is left alone.
func TestRewriteTruncEqualityLeavesOtherConditions(t *testing.T) {
	for _, in := range []string{
		"SELECT 1 FROM sl WHERE date_trunc('day', created_at) >= CURRENT_DATE",
		"SELECT 1 FROM sl WHERE date_trunc('day', created_at + interval '1 hour') = CURRENT_DATE",
		"SELECT 1 FROM sl WHERE date_trunc('fortnight', created_at) = CURRENT_DATE",
		"SELECT 'date_trunc(''day'', created_at) = 1' FROM sl",
		"SELECT 1 FROM sl WHERE created_at = CURRENT_DATE",
	} {
		if got, ok := RewriteTruncEquality(in, func(string) string { return "timestamp" }); ok {
			t.Errorf("%q rewritten to %q", in, got)
		}
	}
}

// A parameter added to an interval would be taken for an interval: without
// the column's type there is no correct rewrite, and the condition stays.
func TestRewriteTruncEqualityNeedsTheTypeForAParameter(t *testing.T) {
	in := "SELECT 1 FROM sl WHERE date_trunc('day', created_at) = {{.day}}"
	if got, ok := RewriteTruncEquality(in, func(string) string { return "" }); ok {
		t.Errorf("rewritten to %q", got)
	}
}

// A query written over lines keeps its line breaks.
func TestRewriteTruncEqualityKeepsTheLayout(t *testing.T) {
	got, _ := RewriteTruncEquality("SELECT 1\nFROM sl\nWHERE date_trunc('day', created_at) = CURRENT_DATE\nORDER BY 1", nil)
	want := "SELECT 1\nFROM sl\nWHERE (created_at >= (CURRENT_DATE) AND created_at < (CURRENT_DATE) + interval '1 day')\nORDER BY 1"
	if got != want {
		t.Errorf("got %q", got)
	}
}

// The plan's total cost is the planner's own estimate of the work, which is
// what a speed check compares a rewrite against. It is the root's.
func TestPlanCostIsTheRootsTotalCost(t *testing.T) {
	got, err := PlanCost([]byte(`[{"Plan": {"Node Type": "Hash Join", "Total Cost": 1250.5, "Plans": [{"Node Type": "Seq Scan", "Total Cost": 900}]}}]`))
	if err != nil || got != 1250.5 {
		t.Errorf("cost = %v, %v", got, err)
	}
}
