// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

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
