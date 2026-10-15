// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package sqlite

const (
	QueryUpsert      = "Upsert"
	QueryDelete      = "Delete"
	QueryListTables  = "ListTables"
	QueryBrowse      = "Browse"
	QueryCreateTable = "CreateTable"
	QueryListColumns = "ListColumns"
)

var commonQueries = map[string]string{
	QueryUpsert:      "INSERT OR REPLACE INTO %s (id, data) VALUES (?, ?)",
	QueryDelete:      "DELETE FROM %s WHERE id = ?",
	QueryListTables:  "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%%'",
	QueryBrowse:      "SELECT * FROM %s LIMIT %d",
	QueryCreateTable: "CREATE TABLE IF NOT EXISTS %s (id TEXT PRIMARY KEY, data TEXT)",
	QueryListColumns: "PRAGMA table_info(%s)",
}
