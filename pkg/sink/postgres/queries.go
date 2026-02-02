package postgres

const (
	QueryUpsert        = "Upsert"
	QueryDelete        = "Delete"
	QueryListDatabases = "ListDatabases"
	QueryListTables    = "ListTables"
	QueryBrowse        = "Browse"
)

var commonQueries = map[string]string{
	QueryUpsert:        "INSERT INTO %s (id, data) VALUES ($1, $2) ON CONFLICT (id) DO UPDATE SET data = $2",
	QueryDelete:        "DELETE FROM %s WHERE id = $1",
	QueryListDatabases: "SELECT datname FROM pg_database WHERE datistemplate = false",
	QueryListTables:    "SELECT table_schema || '.' || table_name FROM information_schema.tables WHERE table_schema NOT IN ('information_schema', 'pg_catalog')",
	QueryBrowse:        "SELECT * FROM %s LIMIT %d",
}
