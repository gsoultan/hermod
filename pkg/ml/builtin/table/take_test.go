// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

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
