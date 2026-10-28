// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package control

import (
	"fmt"
	"testing"
)

// BranchNames lists the branches a switch or router can send a message down,
// named exactly as its executor names them, so the engine can check the
// workflow's edges against them before any message arrives.
func TestBranchNamesListsEveryCaseAndRule(t *testing.T) {
	for _, tc := range []struct {
		nodeType string
		config   map[string]any
		want     []string
	}{
		{"switch", map[string]any{"cases": []any{
			map[string]any{"label": "REVISION"}, map[string]any{"label": ""},
		}}, []string{"REVISION", "case_1"}},
		{"switch", map[string]any{"cases": `[{"label":"SUBMIT"}]`}, []string{"SUBMIT"}},
		{"router", map[string]any{"rules": []any{
			map[string]any{"label": ""}, map[string]any{"label": "eu"},
		}}, []string{"rule_0", "eu"}},
		{"condition", map[string]any{}, nil},
	} {
		got := BranchNames(tc.nodeType, tc.config)
		if fmt.Sprint(got) != fmt.Sprint(tc.want) {
			t.Errorf("BranchNames(%s, %v) = %v, want %v", tc.nodeType, tc.config, got, tc.want)
		}
	}
}
