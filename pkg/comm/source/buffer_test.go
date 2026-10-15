// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

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
