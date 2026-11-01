// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package sql

import (
	"strings"
	"testing"
)

// The reconciliation loop asks every five seconds, on every worker, for the
// suspended messages that are due: "WHERE resume_at <= ?". The table had no
// index on resume_at, so each tick was a full scan of every parked message in
// the deployment -- payload BLOBs included -- however many of them were due.
func TestListSuspendedMessages_DueScanUsesAnIndex(t *testing.T) {
	st := newDashboardHistoryStorage(t).(*sqlStorage)

	rows, err := st.db.QueryContext(t.Context(),
		"EXPLAIN QUERY PLAN "+st.prepareQuery(st.queries.get(QueryListSuspendedMessages)), 0)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	defer rows.Close()

	var plan []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatalf("scan plan: %v", err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read plan: %v", err)
	}
	joined := strings.Join(plan, "\n")
	if !strings.Contains(joined, "USING INDEX idx_suspended_messages_resume_at") {
		t.Fatalf("the due-message scan does not use idx_suspended_messages_resume_at; plan:\n%s", joined)
	}
}
