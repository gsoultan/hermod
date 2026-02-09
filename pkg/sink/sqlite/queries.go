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
