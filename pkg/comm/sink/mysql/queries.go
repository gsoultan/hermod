// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package mysql

const (
	QueryUpsert         = "Upsert"
	QueryDelete         = "Delete"
	QueryShowDatabases  = "ShowDatabases"
	QueryShowTables     = "ShowTables"
	QuerySample         = "Sample"
	QueryBrowse         = "Browse"
	QueryCreateTable    = "CreateTable"
	QueryCreateDatabase = "CreateDatabase"
	QueryListColumns    = "ListColumns"
)

var commonQueries = map[string]string{
	QueryUpsert:         "INSERT INTO %s (id, data) VALUES (?, ?) ON DUPLICATE KEY UPDATE data = VALUES(data)",
	QueryDelete:         "DELETE FROM %s WHERE id = ?",
	QueryShowDatabases:  "SHOW DATABASES",
	QueryShowTables:     "SHOW TABLES",
	QuerySample:         "SELECT * FROM %s LIMIT 1",
	QueryBrowse:         "SELECT * FROM %s LIMIT %d",
	QueryCreateTable:    "CREATE TABLE IF NOT EXISTS %s (id VARCHAR(255) PRIMARY KEY, data JSON)",
	QueryCreateDatabase: "CREATE DATABASE IF NOT EXISTS %s",
	QueryListColumns:    "SELECT COLUMN_NAME, DATA_TYPE, COALESCE(IS_NULLABLE = 'YES', 0), COALESCE(COLUMN_KEY = 'PRI', 0), COALESCE(EXTRA = 'auto_increment', 0), COLUMN_DEFAULT FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_NAME = ? AND TABLE_SCHEMA = DATABASE() ORDER BY ORDINAL_POSITION",
}
