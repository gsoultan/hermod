// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

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
