// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package learn

import "math/rand/v2"

// newRand is the random source of one part of a training: a PCG seeded with
// the training's seed, on a stream of its own, so the same seed draws the
// same numbers every time.
func newRand(seed, stream uint64) *rand.Rand {
	return rand.New(rand.NewPCG(seed, stream)) //nolint:gosec // G404: seeded on purpose, so a training repeats; nothing relies on it being unpredictable.
}
