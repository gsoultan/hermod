package eventstore

type queryRegistry struct {
	driver string
}

func newQueryRegistry(driver string) *queryRegistry {
	return &queryRegistry{driver: driver}
}

func (r *queryRegistry) get(key string) string {
	if driverQueries, ok := driverOverrides[r.driver]; ok {
		if q, ok := driverQueries[key]; ok {
			return q
		}
	}
	return commonQueries[key]
}

const (
	QueryInitSchema    = "InitSchema"
	QueryGetLastOffset = "GetLastOffset"
	QueryInsertEvent   = "InsertEvent"
	QueryReadAll       = "ReadAll"
)

var commonQueries = map[string]string{
	QueryInitSchema: `
			CREATE TABLE IF NOT EXISTS event_store (
				global_offset INTEGER PRIMARY KEY AUTOINCREMENT,
				stream_id TEXT NOT NULL,
				stream_offset INTEGER NOT NULL,
				event_type TEXT NOT NULL,
				payload BLOB,
				metadata TEXT,
				timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
				UNIQUE(stream_id, stream_offset)
			);
			CREATE INDEX IF NOT EXISTS idx_event_store_stream_id ON event_store(stream_id);
			CREATE INDEX IF NOT EXISTS idx_event_store_global_offset ON event_store(global_offset);
			CREATE INDEX IF NOT EXISTS idx_event_store_timestamp ON event_store(timestamp);
			CREATE INDEX IF NOT EXISTS idx_event_store_event_type ON event_store(event_type);
		`,
	QueryGetLastOffset: "SELECT MAX(stream_offset) FROM event_store WHERE stream_id = ?",
	QueryInsertEvent: `INSERT INTO event_store (stream_id, stream_offset, event_type, payload, metadata, timestamp) 
					 VALUES (?, ?, ?, ?, ?, ?)`,
	QueryReadAll: "SELECT global_offset, stream_id, stream_offset, event_type, payload, metadata, timestamp FROM event_store WHERE 1=1",
}

var driverOverrides = map[string]map[string]string{
	"postgres": {
		QueryInitSchema: `
			CREATE TABLE IF NOT EXISTS event_store (
				global_offset SERIAL PRIMARY KEY,
				stream_id TEXT NOT NULL,
				stream_offset BIGINT NOT NULL,
				event_type TEXT NOT NULL,
				payload BYTEA,
				metadata JSONB,
				timestamp TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
				UNIQUE(stream_id, stream_offset)
			);
			CREATE INDEX IF NOT EXISTS idx_event_store_stream_id ON event_store(stream_id);
			CREATE INDEX IF NOT EXISTS idx_event_store_global_offset ON event_store(global_offset);
			CREATE INDEX IF NOT EXISTS idx_event_store_timestamp ON event_store(timestamp);
			CREATE INDEX IF NOT EXISTS idx_event_store_event_type ON event_store(event_type);
		`,
		QueryGetLastOffset: "SELECT MAX(stream_offset) FROM event_store WHERE stream_id = $1",
		QueryInsertEvent: `INSERT INTO event_store (stream_id, stream_offset, event_type, payload, metadata, timestamp) 
					 VALUES ($1, $2, $3, $4, $5, $6)`,
	},
}
