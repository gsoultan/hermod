package idempotency

const (
	QueryInitTable = "InitTable"
	QueryClaim     = "Claim"
	QueryMarkSent  = "MarkSent"
)

var commonQueries = map[string]string{
	QueryInitTable: `CREATE TABLE IF NOT EXISTS %s (
			key TEXT PRIMARY KEY, 
			status INTEGER NOT NULL, 
			first_seen_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP, 
			last_update TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
			)`,
	QueryClaim:    "INSERT INTO %s (key, status) VALUES (?, 0) ON CONFLICT(key) DO NOTHING",
	QueryMarkSent: "UPDATE %s SET status=1, last_update=CURRENT_TIMESTAMP WHERE key=?",
}
