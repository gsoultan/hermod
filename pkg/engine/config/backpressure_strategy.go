// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

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
