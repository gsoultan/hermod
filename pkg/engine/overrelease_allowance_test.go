// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

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
