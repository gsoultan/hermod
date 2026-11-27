// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package scale

import (
	"testing"
	"time"
)

func TestPercentileNearestRank(t *testing.T) {
	ms := func(n ...int) []time.Duration {
		out := make([]time.Duration, len(n))
		for i, v := range n {
			out[i] = time.Duration(v) * time.Millisecond
		}
		return out
	}
	tests := []struct {
		name   string
		values []time.Duration
		p      float64
		want   time.Duration
	}{
		{"empty", nil, 50, 0},
		{"single", ms(7), 99, 7 * time.Millisecond},
		{"median of ten", ms(10, 1, 9, 2, 8, 3, 7, 4, 6, 5), 50, 5 * time.Millisecond},
		{"p90 of ten", ms(10, 1, 9, 2, 8, 3, 7, 4, 6, 5), 90, 9 * time.Millisecond},
		{"p100 is the max", ms(3, 1, 2), 100, 3 * time.Millisecond},
		{"p0 is the min", ms(3, 1, 2), 0, 1 * time.Millisecond},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := percentile(tc.values, tc.p); got != tc.want {
				t.Fatalf("percentile(%v, %v) = %v, want %v", tc.values, tc.p, got, tc.want)
			}
		})
	}
}

// The input belongs to the caller, who may go on to compute other
// percentiles from it in the order it was collected.
func TestPercentileDoesNotReorderInput(t *testing.T) {
	in := []time.Duration{3, 1, 2}
	_ = percentile(in, 50)
	if in[0] != 3 || in[1] != 1 || in[2] != 2 {
		t.Fatalf("input reordered: %v", in)
	}
}
