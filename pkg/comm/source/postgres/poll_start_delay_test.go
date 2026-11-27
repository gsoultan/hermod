// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package postgres

import (
	"testing"
	"time"
)

// Every polling workflow used to run its first query the moment it started and
// then every interval after, so a thousand workflows on one source database
// started together queried it together, a thousand at once, every interval.
// The first poll now waits a random part of the interval, capped so a long
// interval does not hold back the first rows, and the ticks that follow keep
// that offset.
func TestPollStartDelayIsSpreadWithinTheIntervalAndCapped(t *testing.T) {
	cases := []struct {
		name     string
		interval time.Duration
		max      time.Duration
	}{
		{"short interval", 5 * time.Second, 5 * time.Second},
		{"long interval is capped", time.Hour, maxPollStartDelay},
		{"zero interval", 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			distinct := map[time.Duration]bool{}
			for range 200 {
				d := pollStartDelay(tc.interval)
				if d < 0 || (tc.max > 0 && d >= tc.max) || (tc.max == 0 && d != 0) {
					t.Fatalf("pollStartDelay(%v) = %v; want within [0, %v)", tc.interval, d, tc.max)
				}
				distinct[d] = true
			}
			if tc.max > 0 && len(distinct) < 50 {
				t.Fatalf("pollStartDelay(%v) gave %d distinct delays in 200 calls; starts are not spread", tc.interval, len(distinct))
			}
		})
	}
}
