// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package table

import (
	"reflect"
	"testing"
)

func TestTakePicksRowsInOrder(t *testing.T) {
	tb, err := FromRecords([]map[string]any{{"a": 1.0, "s": "x"}, {"a": nil, "s": "y"}, {"a": 3.0, "s": nil}})
	if err != nil {
		t.Fatal(err)
	}
	got := tb.Take([]int{2, 0}).Records(0, 2)
	want := []map[string]any{{"a": 3.0, "s": nil}, {"a": 1.0, "s": "x"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Take = %v, want %v", got, want)
	}
}
