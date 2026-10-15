// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

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
