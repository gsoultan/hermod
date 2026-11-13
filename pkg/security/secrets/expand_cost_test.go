// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package secrets

import (
	"context"
	"testing"
)

// A running workflow expands every transformation's settings on every
// message, so that a rotated secret is read on the next one. Settings with no
// reference in them — almost all of them — must come back as they are without
// allocating: re-boxing each unchanged string was 8% of all allocations in
// BenchmarkWorkflowThroughput.
func TestExpandSettingsWithoutReferencesAllocatesNothing(t *testing.T) {
	settings := map[string]any{
		"field":    "status",
		"operator": "=",
		"value":    "active",
		"nested":   map[string]any{"a": "b", "n": 3.0},
		"list":     []any{"x", "y"},
	}
	ctx := context.Background()

	n := testing.AllocsPerRun(500, func() {
		out, changed, err := ExpandSettings(ctx, nil, "default", settings, nil)
		if err != nil || changed || len(out) != len(settings) {
			t.Fatalf("out=%v changed=%v err=%v", out, changed, err)
		}
	})
	if n != 0 {
		t.Errorf("expanding settings with no secret reference: %.1f allocations per call, want 0", n)
	}
}
