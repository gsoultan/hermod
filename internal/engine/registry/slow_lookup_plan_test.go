// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package registry

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/gsoultan/hermod/pkg/comm/transformer/lookup"
)

// A lookup that ran out of time on a database that was answering reads its
// query's plan through the registry, so the error says why the query is slow.
var _ lookup.SlowLookupPlanner = (*Registry)(nil)

// A database the plan reader does not handle is not a failure to report: the
// lookup's error then says only what the ping found.
func TestPlanSlowLookupSaysWhenTheDatabaseCannotBePlanned(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	_, err = (&Registry{}).PlanSlowLookup(t.Context(), db, "SELECT 1")
	if !errors.Is(err, lookup.ErrUnplannable) {
		t.Errorf("err = %v, want lookup.ErrUnplannable", err)
	}
}
