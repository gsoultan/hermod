// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package dbactivity

import (
	"testing"
	"time"

	"github.com/gsoultan/hermod"
)

func TestHoldersNamesEachQueryWithoutItsText(t *testing.T) {
	now := time.Now()
	qs := []Query{
		{Kind: SourcePoll, WorkflowID: "nightly", Started: now.Add(-2 * time.Hour)},
		{Kind: SQLStep, WorkflowID: "etl", NodeID: "n4", Started: now.Add(-90 * time.Second)},
	}
	got := Holders(qs, now)
	want := []hermod.Holder{
		{WorkflowID: "nightly", Kind: "source poll", Running: 2 * time.Hour},
		{WorkflowID: "etl", NodeID: "n4", Kind: "SQL step", Running: 90 * time.Second},
	}
	if len(got) != len(want) {
		t.Fatalf("Holders = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Holders[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
	if Holders(nil, now) != nil {
		t.Error("Holders(nil) is not nil")
	}
}
