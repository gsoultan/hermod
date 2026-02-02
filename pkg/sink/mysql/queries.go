package mysql

const (
	QueryUpsert        = "Upsert"
	QueryDelete        = "Delete"
	QueryShowDatabases = "ShowDatabases"
	QueryShowTables    = "ShowTables"
	QuerySample        = "Sample"
	QueryBrowse        = "Browse"
)

var commonQueries = map[string]string{
	QueryUpsert:        "INSERT INTO %s (id, data) VALUES (?, ?) ON DUPLICATE KEY UPDATE data = VALUES(data)",
	QueryDelete:        "DELETE FROM %s WHERE id = ?",
	QueryShowDatabases: "SHOW DATABASES",
	QueryShowTables:    "SHOW TABLES",
	QuerySample:        "SELECT * FROM %s LIMIT 1",
	QueryBrowse:        "SELECT * FROM %s LIMIT %d",
}
