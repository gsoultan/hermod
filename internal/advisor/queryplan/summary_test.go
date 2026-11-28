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
	"strings"
	"testing"
)

// --- A lookup that ran out of time said only "raise the step's time limit" ---
//
// When the database was answering and Hermod's own queries were not in the
// way, the lookup's query itself was the slow part, and the error left the
// operator to find out why. Summary is what the error now carries: the plan's
// verdict in one sentence, with the change that would make it fast.

func TestSummaryNamesTheMissingIndexAndTheFix(t *testing.T) {
	c := Check{Findings: []Finding{
		{Rule: RuleIndexUsed, Severity: SeverityOK, Title: "Uses index so_doc", Detail: "The query reads public.sales_orders through so_doc."},
		{Rule: RuleMissingIndex, Severity: SeverityWarn, Table: "etl.SODTL",
			Title: "No index for the lookup on etl.SODTL", Detail: "The query reads all about 50000 rows of etl.SODTL to find matches on \"ItemNo\".",
			Proposal: `CREATE INDEX CONCURRENTLY sodtl_itemno ON "etl"."SODTL" ("ItemNo");`},
	}}
	got := c.Summary()
	for _, want := range []string{"reads all about 50000 rows of etl.SODTL", `CREATE INDEX CONCURRENTLY sodtl_itemno`} {
		if !strings.Contains(got, want) {
			t.Errorf("summary does not contain %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "so_doc") {
		t.Errorf("summary repeats a table that is already read the right way:\n%s", got)
	}
}

func TestSummaryOffersTheRewriteWhenOneExists(t *testing.T) {
	c := Check{Findings: []Finding{{Rule: RuleWrappedColumn, Severity: SeverityWarn,
		Detail: "The query compares date_trunc('day', created_at), not the column itself.", Rewrite: "created_at >= $1 AND created_at < $1 + interval '1 day'"}}}
	if got := c.Summary(); !strings.Contains(got, "created_at >= $1 AND created_at < $1 + interval '1 day'") {
		t.Errorf("summary does not offer the rewrite:\n%s", got)
	}
}

func TestSummarySaysThePlanIsNotTheCauseWhenEveryTableUsesAnIndex(t *testing.T) {
	c := Check{Findings: []Finding{
		{Rule: RuleIndexUsed, Severity: SeverityOK, Title: "Uses index so_doc"},
		{Rule: RuleSmallTable, Severity: SeverityOK, Title: "Full scan of a small table"},
	}}
	got := c.Summary()
	if !strings.Contains(got, "the plan is not what made it slow") || !strings.Contains(got, "lock") {
		t.Errorf("summary does not say the time went somewhere other than the plan:\n%s", got)
	}
}
