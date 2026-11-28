// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package advisor

import (
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/hermod/pkg/infra/dbactivity"
)

var dbNow = time.Date(2026, 11, 28, 7, 0, 0, 0, time.UTC)

func names(id string) string {
	return map[string]string{"wf-1": "Daily store totals", "wf-2": "Stock report"}[id]
}

func rules(cs []Card) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Rule)
	}
	return out
}

// --- The incident: Hermod's own long queries filled PgBouncer's 12 server
// connections and lookups timed out. Hermod now learns its share and keeps a
// quarter free for lookups. The page says it did that, and says plainly what
// only a person can change. ---

func TestDatabaseSaysWhatHermodFixedAndWhatNeedsAPerson(t *testing.T) {
	use := DatabaseUse{SourceID: "src-1", Source: "athena", Snapshot: dbactivity.Snapshot{
		Ceiling: 15, Learned: 12, Limit: 9, Running: 10, InBulk: 9,
		QueuedAt: dbNow.Add(-20 * time.Minute),
		Queries: []dbactivity.Query{
			{Kind: dbactivity.SQLStep, WorkflowID: "wf-2", NodeID: "report", Started: dbNow.Add(-2 * time.Hour)},
			{Kind: dbactivity.SQLStep, WorkflowID: "wf-1", NodeID: "totals", Started: dbNow.Add(-40 * time.Minute)},
			{Kind: dbactivity.Lookup, WorkflowID: "wf-1", NodeID: "find", Started: dbNow.Add(-time.Second)},
		},
	}}
	got := ReadDatabase(use, "wf-1", names, dbNow)

	if r := rules(got.Fixed); strings.Join(r, ",") != "learned_share,kept_lookups_moving" {
		t.Fatalf("fixed = %v", r)
	}
	if d := got.Fixed[0].Detail; !strings.Contains(d, "12 connections") || !strings.Contains(d, "9 long queries") || !strings.Contains(d, "3 free for lookups") {
		t.Errorf("learned detail = %q", d)
	}
	if d := got.Fixed[1].Detail; !strings.Contains(d, "20 min ago") {
		t.Errorf("queued detail = %q", d)
	}

	if r := rules(got.NeedsYou); strings.Join(r, ",") != "long_query,other_flows,pool_smaller_than_set" {
		t.Fatalf("needs you = %v", r)
	}
	long := got.NeedsYou[0]
	if long.NodeID != "totals" || !strings.Contains(long.Title, "40 min") || !strings.Contains(long.Detail, "Speed") {
		t.Errorf("long query card = %+v", long)
	}
	if d := got.NeedsYou[1].Detail; !strings.Contains(d, "Stock report") || !strings.Contains(d, "2.0 h") {
		t.Errorf("other flows detail = %q", d)
	}
	if d := got.NeedsYou[2].Detail; !strings.Contains(d, "pool_size") || !strings.Contains(d, "12") {
		t.Errorf("pool detail = %q", d)
	}
}

// A quiet database: nothing learned, nothing long, nothing queued. No card
// is made up to fill the page.
func TestAQuietDatabaseHasNoCards(t *testing.T) {
	use := DatabaseUse{SourceID: "src-1", Source: "athena", Snapshot: dbactivity.Snapshot{Ceiling: 15, Limit: 15,
		Queries: []dbactivity.Query{{Kind: dbactivity.SQLStep, WorkflowID: "wf-1", NodeID: "totals", Started: dbNow.Add(-time.Minute)}}}}
	got := ReadDatabase(use, "wf-1", names, dbNow)
	if len(got.Fixed) != 0 || len(got.NeedsYou) != 0 {
		t.Errorf("cards = %+v / %+v", got.Fixed, got.NeedsYou)
	}
}

// A wait seen hours ago is history, not something to report as now.
func TestAnOldWaitIsNotReported(t *testing.T) {
	use := DatabaseUse{Source: "athena", Snapshot: dbactivity.Snapshot{Ceiling: 15, Limit: 15, QueuedAt: dbNow.Add(-3 * time.Hour)}}
	if got := ReadDatabase(use, "wf-1", names, dbNow); len(got.Fixed) != 0 {
		t.Errorf("fixed = %v", rules(got.Fixed))
	}
}
