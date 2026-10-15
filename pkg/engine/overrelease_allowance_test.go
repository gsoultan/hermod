// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

package engine

import "sync/atomic"

// A handful of tests deliberately over-release a pendingMessage to prove the
// idempotent guard in releasePendingMessage holds — that is their whole point.
// Those releases are expected, so they must not make TestMain's tripwire fire,
// but they also must not be waved through by loosening it: a test that declares
// an allowance and then over-releases a *different* number of times is still a
// bug worth catching.
//
// Each such test declares exactly how many it will perform, and TestMain
// subtracts the declared total. Any release beyond the declared budget — from
// any test in the package — fails the run.
var expectedPendingOverReleases atomic.Int64

// allowPendingOverReleases declares that the calling test intentionally
// over-releases a pendingMessage n times.
func allowPendingOverReleases(n int64) {
	expectedPendingOverReleases.Add(n)
}
