// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package dbactivity

// BulkLimit is how many bulk queries may run at once on a database allowed
// maxConns connections, keeping reserve of them for lookups. reserve < 0 means
// the default: a quarter of maxConns (3 of 12), at least one once there are two
// connections to share. A reserve is clamped so at least one bulk query can
// always run. maxConns <= 0 means the database's limit is not known, and bulk
// queries are not bounded (0).
func BulkLimit(maxConns, reserve int) int {
	if maxConns <= 0 {
		return 0
	}
	if reserve < 0 {
		reserve = maxConns / 4
		if maxConns >= 2 {
			reserve = max(reserve, 1)
		}
	}
	return max(maxConns-reserve, 1)
}
