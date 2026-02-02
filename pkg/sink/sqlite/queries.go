package sqlite

const (
	QueryUpsert     = "Upsert"
	QueryDelete     = "Delete"
	QueryListTables = "ListTables"
	QueryBrowse     = "Browse"
)

var commonQueries = map[string]string{
	QueryUpsert:     "INSERT OR REPLACE INTO %s (id, data) VALUES (?, ?)",
	QueryDelete:     "DELETE FROM %s WHERE id = ?",
	QueryListTables: "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%%'",
	QueryBrowse:     "SELECT * FROM %s LIMIT %d",
}
