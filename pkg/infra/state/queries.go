// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

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
