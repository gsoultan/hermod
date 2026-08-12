package idempotency

const (
	QueryInitTable = "InitTable"
	QueryClaim     = "Claim"
	QueryMarkSent  = "MarkSent"
	QueryRelease   = "Release"
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

	// Only an unsent claim is released. status is what distinguishes a claim
	// taken before the work from one the work completed, and releasing a
	// completed key would let a genuine duplicate through — the column was
	// written and never read until now, which is why nothing could tell the two
	// apart.
	QueryRelease: "DELETE FROM %s WHERE key=? AND status=0",
}
