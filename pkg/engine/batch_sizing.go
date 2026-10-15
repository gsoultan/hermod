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
