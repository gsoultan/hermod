// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

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
