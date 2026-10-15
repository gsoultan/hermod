// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package engine

import "testing"

// A batch can only ever be filled from messages that are currently in flight.
// When BatchSize exceeds MaxInflight the batch can never fill, so every flush
// waits out the full BatchTimeout and throughput collapses — measured at 2,557
// msgs/s versus 110,852 msgs/s for an otherwise identical configuration
// (see BenchmarkBatchVsInflight).
//
// effectiveBatchSize clamps the configured size to what is actually reachable.
func TestEffectiveBatchSize(t *testing.T) {
	tests := []struct {
		name        string
		batchSize   int
		maxInflight int
		want        int
	}{
		{"batch below inflight is untouched", 100, 128, 100},
		{"batch equal to inflight is untouched", 128, 128, 128},
		{"batch above inflight is clamped", 500, 128, 128},
		{"README default combo is clamped", 500, 128, 128},
		{"unbatched stays unbatched", 0, 128, 0},
		{"negative batch stays unbatched", -1, 128, -1},
		{"non-positive inflight disables clamping", 500, 0, 500},
		{"negative inflight disables clamping", 500, -5, 500},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := effectiveBatchSize(tc.batchSize, tc.maxInflight)
			if got != tc.want {
				t.Errorf("effectiveBatchSize(%d, %d) = %d; want %d",
					tc.batchSize, tc.maxInflight, got, tc.want)
			}
		})
	}
}
