// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package source

import "testing"

// TestDefaultSourceBuffer guards the lightweight memory budget: the per-source
// channel must stay shallow so connectors apply backpressure instead of
// hoarding messages in RAM. A regression that bumps this back to the old
// 1000/1024 depth would silently inflate the resident set.
func TestDefaultSourceBuffer(t *testing.T) {
	if DefaultSourceBuffer <= 0 {
		t.Fatalf("DefaultSourceBuffer must be positive, got %d", DefaultSourceBuffer)
	}
	if DefaultSourceBuffer > 128 {
		t.Fatalf("DefaultSourceBuffer = %d; expected a small bounded buffer (<=128)", DefaultSourceBuffer)
	}
}
