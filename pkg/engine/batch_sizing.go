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

// effectiveBatchSize returns the batch size a sink writer can actually reach.
//
// A batch is filled from messages currently in flight, and the engine caps
// in-flight messages at MaxInflight to bound memory. If BatchSize exceeds that
// cap the batch can never fill on count alone, so every flush falls through to
// the BatchTimeout path. The result is a throughput collapse — 2,557 msgs/s
// versus 110,852 msgs/s for the same workload with a reachable batch size
// (BenchmarkBatchVsInflight).
//
// Clamping down rather than raising MaxInflight is deliberate: MaxInflight
// exists to bound memory, so silently raising it would trade an invisible
// latency problem for an invisible memory one.
//
// A non-positive maxInflight means "uncapped" and disables clamping. A
// non-positive batchSize means the sink is unbatched and is returned unchanged.
func effectiveBatchSize(batchSize, maxInflight int) int {
	if batchSize <= 0 || maxInflight <= 0 {
		return batchSize
	}
	if batchSize > maxInflight {
		return maxInflight
	}
	return batchSize
}
