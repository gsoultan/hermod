package state

const (
	QueryInitTable = "InitTable"
	QueryGet       = "Get"
	QuerySet       = "Set"
	QueryDelete    = "Delete"
)

var commonQueries = map[string]string{
	QueryInitTable: `CREATE TABLE IF NOT EXISTS states (key TEXT PRIMARY KEY, value BLOB)`,
	QueryGet:       `SELECT value FROM states WHERE key = ?`,
	QuerySet:       `INSERT INTO states (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
	QueryDelete:    `DELETE FROM states WHERE key = ?`,
}

var driverOverrides = map[string]map[string]string{
	"mysql": {
		QuerySet: `INSERT INTO states (key, value) VALUES (?, ?) ON DUPLICATE KEY UPDATE value=VALUES(value)`,
	},
}
