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

// DefaultSourceBuffer is the capacity used for the per-source message channel
// that connectors place decoded change events onto.
//
// It is intentionally small: a shallow buffer absorbs short bursts while
// applying backpressure to the producer (the upstream database/stream reader)
// as soon as the downstream sink lags, instead of hoarding up to a thousand
// fully-decoded messages in RAM per active pipeline. With multi-KB payloads
// and several concurrent pipelines, deep buffers were a primary driver of the
// resident set; bounding them keeps Hermod within its lightweight footprint.
const DefaultSourceBuffer = 64
