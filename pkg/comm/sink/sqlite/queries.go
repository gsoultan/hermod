// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

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
