// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

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
