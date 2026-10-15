// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

package state

const (
	QueryInitTable = "InitTable"
	QueryGet       = "Get"
	QuerySet       = "Set"
	QueryDelete    = "Delete"
)

var commonQueries = map[string]string{
	QueryInitTable: `CREATE TABLE IF NOT EXISTS states (key TEXT PRIMARY KEY, value BLOB)`,
	QueryGet:       `SELECT value FROM states WHERE key = ?`,
	QuerySet:       `INSERT INTO states (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
	QueryDelete:    `DELETE FROM states WHERE key = ?`,
}
