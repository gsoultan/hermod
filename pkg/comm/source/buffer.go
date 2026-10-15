// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

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
