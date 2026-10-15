// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package config

// BackpressureStrategy defines how to handle overflow when a sink is slow.
type BackpressureStrategy string

const (
	// BPBlock blocks the source until there is room in the buffer.
	BPBlock BackpressureStrategy = "block"
	// BPDropOldest drops the oldest message in the buffer to make room.
	BPDropOldest BackpressureStrategy = "drop_oldest"
	// BPDropNewest drops the incoming message if the buffer is full.
	BPDropNewest BackpressureStrategy = "drop_newest"
	// BPSampling drops the incoming message with a configured probability.
	BPSampling BackpressureStrategy = "sampling"
	// BPSpillToDisk writes the incoming message to disk if the buffer is full.
	BPSpillToDisk BackpressureStrategy = "spill_to_disk"
)
