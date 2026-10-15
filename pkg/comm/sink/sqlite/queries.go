// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

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
