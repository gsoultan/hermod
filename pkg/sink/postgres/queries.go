package postgres

const (
	QueryUpsert        = "Upsert"
	QueryDelete        = "Delete"
	QueryListDatabases = "ListDatabases"
	QueryListTables    = "ListTables"
	QueryBrowse        = "Browse"
	QueryCreateTable   = "CreateTable"
	QueryCreateSchema  = "CreateSchema"
	QueryListColumns   = "ListColumns"
)

var commonQueries = map[string]string{
	QueryUpsert:        "INSERT INTO %s (id, data) VALUES ($1, $2) ON CONFLICT (id) DO UPDATE SET data = $2",
	QueryDelete:        "DELETE FROM %s WHERE id = $1",
	QueryListDatabases: "SELECT datname FROM pg_database WHERE datistemplate = false",
	QueryListTables:    "SELECT table_schema || '.' || table_name FROM information_schema.tables WHERE table_schema NOT IN ('information_schema', 'pg_catalog')",
	QueryBrowse:        "SELECT * FROM %s LIMIT %d",
	QueryCreateTable:   "CREATE TABLE IF NOT EXISTS %s (id TEXT PRIMARY KEY, data JSONB)",
	QueryCreateSchema:  "CREATE SCHEMA IF NOT EXISTS %s",
	QueryListColumns:   "SELECT column_name, data_type, COALESCE(is_nullable = 'YES', false), EXISTS (SELECT 1 FROM information_schema.key_column_usage kcu JOIN information_schema.table_constraints tc ON kcu.constraint_name = tc.constraint_name WHERE (kcu.table_name = $1 OR kcu.table_schema || '.' || kcu.table_name = $1) AND tc.constraint_type = 'PRIMARY KEY' AND kcu.column_name = columns.column_name), COALESCE(is_identity = 'YES' OR column_default LIKE 'nextval%%', false), column_default FROM information_schema.columns WHERE table_name = $1 OR table_schema || '.' || table_name = $1 ORDER BY ordinal_position",
}
