package storage

// What each list search matches.
//
// These live here rather than in the backends because they were hand-mirrored
// in two packages and drifted: workflows matched name alone in SQL and _id,
// name and vhost in mongo; users matched role in SQL and not in mongo; workers
// matched description in SQL and not in mongo; logs matched action in SQL and
// data, user_id and username in mongo. Which backend was deployed therefore
// changed what the search box found, and nothing failed when the two lists
// disagreed — the only way to notice was to run both and compare.
//
// One list per entity, read by both backends, is what makes that impossible
// rather than merely fixed. Fields are named as the SQL column; the mongo
// backend renames id to _id on the way past, in searchAcross, so no caller has
// to remember to.
var (
	WorkflowSearchFields  = []string{"id", "name", "vhost"}
	ConnectorSearchFields = []string{"id", "name", "type", "vhost"}
	UserSearchFields      = []string{"id", "username", "full_name", "email", "role"}
	VHostSearchFields     = []string{"id", "name", "description"}
	WorkerSearchFields    = []string{"id", "name", "host", "description"}

	// LogSearchFields deliberately omits data. It is the largest column on the
	// largest table Hermod owns, a LIKE over it cannot use an index, and a log
	// search that scans every payload is a log search nobody waits for. The
	// mongo backend used to include it; this is the one place the two were
	// reconciled by taking the narrower list rather than the union.
	LogSearchFields = []string{"message", "action", "source_id", "sink_id", "workflow_id", "user_id", "username"}

	AuditLogSearchFields = []string{"id", "username", "action", "entity_id", "payload"}
)
