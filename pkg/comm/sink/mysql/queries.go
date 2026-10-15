// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

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
