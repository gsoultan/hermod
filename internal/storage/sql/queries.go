package sql

// queryRegistry holds all SQL queries used by the storage.
// It allows for driver-specific overrides and keeps SQL logic separated from Go code.
type queryRegistry struct {
	driver string
}

func newQueryRegistry(driver string) *queryRegistry {
	return &queryRegistry{driver: driver}
}

// get returns the query for the given key, favoring driver-specific versions if they exist.
func (r *queryRegistry) get(key string) string {
	if driverQueries, ok := driverOverrides[r.driver]; ok {
		if q, ok := driverQueries[key]; ok {
			return q
		}
	}
	return commonQueries[key]
}

const (
	// Table creation
	QueryInitSourcesTable            = "InitSourcesTable"
	QueryInitSinksTable              = "InitSinksTable"
	QueryInitUsersTable              = "InitUsersTable"
	QueryInitVHostsTable             = "InitVHostsTable"
	QueryInitWorkersTable            = "InitWorkersTable"
	QueryInitLogsTable               = "InitLogsTable"
	QueryInitWorkflowsTable          = "InitWorkflowsTable"
	QueryInitWorkflowNodeStatesTable = "InitWorkflowNodeStatesTable"
	QueryInitWebhookRequestsTable    = "InitWebhookRequestsTable"
	QueryInitSettingsTable           = "InitSettingsTable"
	QueryInitAuditLogsTable          = "InitAuditLogsTable"
	QueryInitSchemasTable            = "InitSchemasTable"
	QueryInitMessageTraceStepsTable  = "InitMessageTraceStepsTable"
	QueryInitWorkflowVersionsTable   = "InitWorkflowVersionsTable"
	QueryInitOutboxTable             = "InitOutboxTable"
	QueryInitWorkspacesTable         = "InitWorkspacesTable"
	QueryInitPluginsTable            = "InitPluginsTable"

	// Upserts
	QueryUpdateNodeState = "UpdateNodeState"
	QuerySaveSetting     = "SaveSetting"

	// Sources
	QueryListSources        = "ListSources"
	QueryCountSources       = "CountSources"
	QueryCreateSource       = "CreateSource"
	QueryUpdateSource       = "UpdateSource"
	QueryUpdateSourceStatus = "UpdateSourceStatus"
	QueryUpdateSourceState  = "UpdateSourceState"
	QueryDeleteSource       = "DeleteSource"
	QueryGetSource          = "GetSource"

	// Sinks
	QueryListSinks        = "ListSinks"
	QueryCountSinks       = "CountSinks"
	QueryCreateSink       = "CreateSink"
	QueryUpdateSink       = "UpdateSink"
	QueryUpdateSinkStatus = "UpdateSinkStatus"
	QueryDeleteSink       = "DeleteSink"
	QueryGetSink          = "GetSink"

	// Users
	QueryListUsers            = "ListUsers"
	QueryCountUsers           = "CountUsers"
	QueryCreateUser           = "CreateUser"
	QueryUpdateUser           = "UpdateUser"
	QueryUpdateUserNoPassword = "UpdateUserNoPassword"
	QueryDeleteUser           = "DeleteUser"
	QueryGetUser              = "GetUser"
	QueryGetUserByUsername    = "GetUserByUsername"
	QueryGetUserByEmail       = "GetUserByEmail"

	// VHosts
	QueryListVHosts  = "ListVHosts"
	QueryCountVHosts = "CountVHosts"
	QueryCreateVHost = "CreateVHost"
	QueryDeleteVHost = "DeleteVHost"
	QueryGetVHost    = "GetVHost"

	// Workflows
	QueryListWorkflows        = "ListWorkflows"
	QueryCountWorkflows       = "CountWorkflows"
	QueryCreateWorkflow       = "CreateWorkflow"
	QueryUpdateWorkflow       = "UpdateWorkflow"
	QueryUpdateWorkflowStatus = "UpdateWorkflowStatus"
	QueryDeleteWorkflow       = "DeleteWorkflow"
	QueryGetWorkflow          = "GetWorkflow"
	QueryAcquireLease         = "AcquireLease"
	QueryRenewLease           = "RenewLease"
	QueryReleaseLease         = "ReleaseLease"

	// Workspaces
	QueryListWorkspaces  = "ListWorkspaces"
	QueryCreateWorkspace = "CreateWorkspace"
	QueryDeleteWorkspace = "DeleteWorkspace"
	QueryGetWorkspace    = "GetWorkspace"

	// Workers
	QueryListWorkers     = "ListWorkers"
	QueryCountWorkers    = "CountWorkers"
	QueryCreateWorker    = "CreateWorker"
	QueryUpdateWorker    = "UpdateWorker"
	QueryUpdateHeartbeat = "UpdateHeartbeat"
	QueryDeleteWorker    = "DeleteWorker"
	QueryGetWorker       = "GetWorker"

	// Logs
	QueryListLogs   = "ListLogs"
	QueryCountLogs  = "CountLogs"
	QueryCreateLog  = "CreateLog"
	QueryDeleteLogs = "DeleteLogs"

	// Settings
	QueryGetSetting = "GetSetting"

	// Audit Logs
	QueryCreateAuditLog     = "CreateAuditLog"
	QueryListAuditLogs      = "ListAuditLogs"
	QueryCountAuditLogs     = "CountAuditLogs"
	QueryPurgeAuditLogs     = "PurgeAuditLogs"
	QueryPurgeMessageTraces = "PurgeMessageTraces"

	// Webhook Requests
	QueryCreateWebhookRequest  = "CreateWebhookRequest"
	QueryListWebhookRequests   = "ListWebhookRequests"
	QueryCountWebhookRequests  = "CountWebhookRequests"
	QueryGetWebhookRequest     = "GetWebhookRequest"
	QueryDeleteWebhookRequests = "DeleteWebhookRequests"

	// Form Submissions
	QueryInitFormSubmissionsTable   = "InitFormSubmissionsTable"
	QueryCreateFormSubmission       = "CreateFormSubmission"
	QueryListFormSubmissions        = "ListFormSubmissions"
	QueryCountFormSubmissions       = "CountFormSubmissions"
	QueryGetFormSubmission          = "GetFormSubmission"
	QueryUpdateFormSubmissionStatus = "UpdateFormSubmissionStatus"
	QueryDeleteFormSubmissions      = "DeleteFormSubmissions"

	// Schemas
	QueryListSchemas     = "ListSchemas"
	QueryListAllSchemas  = "ListAllSchemas"
	QueryGetSchema       = "GetSchema"
	QueryGetLatestSchema = "GetLatestSchema"
	QueryCreateSchema    = "CreateSchema"

	// Tracing
	QueryRecordTraceStep   = "RecordTraceStep"
	QueryGetMessageTrace   = "GetMessageTrace"
	QueryListMessageTraces = "ListMessageTraces"

	// Workflow Versioning
	QueryCreateWorkflowVersion = "CreateWorkflowVersion"
	QueryListWorkflowVersions  = "ListWorkflowVersions"
	QueryGetWorkflowVersion    = "GetWorkflowVersion"

	// Outbox
	QueryCreateOutboxItem = "CreateOutboxItem"
	QueryListOutboxItems  = "ListOutboxItems"
	QueryDeleteOutboxItem = "DeleteOutboxItem"
	QueryUpdateOutboxItem = "UpdateOutboxItem"

	// Marketplace
	QueryListPlugins     = "ListPlugins"
	QueryGetPlugin       = "GetPlugin"
	QueryCreatePlugin    = "CreatePlugin"
	QueryUpdatePlugin    = "UpdatePlugin"
	QueryInstallPlugin   = "InstallPlugin"
	QueryUninstallPlugin = "UninstallPlugin"
)

var commonQueries = map[string]string{
	QueryInitSourcesTable: `CREATE TABLE IF NOT EXISTS sources (
            id TEXT PRIMARY KEY,
            name TEXT NOT NULL UNIQUE,
            type TEXT,
            vhost TEXT,
            active BOOLEAN DEFAULT FALSE,
            status TEXT,
            worker_id TEXT,
            workspace_id TEXT,
            config TEXT,
            state TEXT,
            sample TEXT
        )`,
	QueryInitSinksTable: `CREATE TABLE IF NOT EXISTS sinks (
            id TEXT PRIMARY KEY,
            name TEXT NOT NULL UNIQUE,
            type TEXT,
            vhost TEXT,
            active BOOLEAN DEFAULT FALSE,
            status TEXT,
            worker_id TEXT,
            workspace_id TEXT,
            config TEXT
        )`,
	QueryInitUsersTable: `CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			username TEXT UNIQUE,
			password TEXT,
			full_name TEXT,
			email TEXT,
			role TEXT,
			vhosts TEXT,
			two_factor_enabled BOOLEAN DEFAULT FALSE,
			two_factor_secret TEXT
		)`,
	QueryInitVHostsTable: `CREATE TABLE IF NOT EXISTS vhosts (
			id TEXT PRIMARY KEY,
			name TEXT UNIQUE,
			description TEXT
		)`,
	QueryInitWorkersTable: `CREATE TABLE IF NOT EXISTS workers (
			id TEXT PRIMARY KEY,
			name TEXT,
			host TEXT,
			port INTEGER,
			description TEXT,
			token TEXT,
			last_seen TIMESTAMP,
			cpu_usage REAL,
			memory_usage REAL
		)`,
	QueryInitLogsTable: `CREATE TABLE IF NOT EXISTS logs (
			id TEXT PRIMARY KEY,
			timestamp TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			level TEXT,
			message TEXT,
			action TEXT,
			source_id TEXT,
			sink_id TEXT,
			workflow_id TEXT,
			user_id TEXT,
			username TEXT,
			data TEXT
		)`,
	QueryInitWorkflowsTable: `CREATE TABLE IF NOT EXISTS workflows (
            id TEXT PRIMARY KEY,
            name TEXT,
            vhost TEXT,
            active BOOLEAN,
            status TEXT,
            worker_id TEXT,
            owner_id TEXT,
            lease_until TIMESTAMP,
            nodes TEXT,
            edges TEXT,
            dead_letter_sink_id TEXT,
            prioritize_dlq BOOLEAN DEFAULT FALSE,
            max_retries INTEGER DEFAULT 0,
            retry_interval TEXT,
            reconnect_interval TEXT,
            dry_run BOOLEAN DEFAULT FALSE,
            retention_days INTEGER,
            schema_type TEXT,
            schema TEXT,
            cron TEXT,
            idle_timeout TEXT,
            tier TEXT,
            trace_sample_rate REAL,
            dlq_threshold INTEGER DEFAULT 0,
            tags TEXT,
            workspace_id TEXT,
            trace_retention TEXT,
            audit_retention TEXT,
            cpu_request REAL DEFAULT 0,
            memory_request REAL DEFAULT 0,
            throughput_request INTEGER DEFAULT 0
        )`,
	QueryInitWorkflowNodeStatesTable: `CREATE TABLE IF NOT EXISTS workflow_node_states (
			workflow_id TEXT,
			node_id TEXT,
			state TEXT,
			PRIMARY KEY (workflow_id, node_id)
		)`,
	QueryInitWebhookRequestsTable: `CREATE TABLE IF NOT EXISTS webhook_requests (
			id TEXT PRIMARY KEY,
			timestamp TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			path TEXT,
			method TEXT,
			headers TEXT,
			body BLOB
		)`,
	QueryInitFormSubmissionsTable: `CREATE TABLE IF NOT EXISTS form_submissions (
			id TEXT PRIMARY KEY,
			timestamp TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			path TEXT,
			data BLOB,
			status TEXT DEFAULT 'pending'
		)`,
	QueryInitSettingsTable: `CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
	QueryInitAuditLogsTable: `CREATE TABLE IF NOT EXISTS audit_logs (
			id TEXT PRIMARY KEY,
			timestamp TIMESTAMP NOT NULL,
			user_id TEXT NOT NULL,
			username TEXT NOT NULL,
			action TEXT NOT NULL,
			entity_type TEXT NOT NULL,
			entity_id TEXT NOT NULL,
			payload TEXT,
			ip TEXT
		)`,
	QueryInitSchemasTable: `CREATE TABLE IF NOT EXISTS schemas (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			version INTEGER NOT NULL,
			type TEXT NOT NULL,
			content TEXT NOT NULL,
			created_at TIMESTAMP NOT NULL,
			UNIQUE(name, version)
		)`,
	QueryInitMessageTraceStepsTable: `CREATE TABLE IF NOT EXISTS message_trace_steps (
			id TEXT PRIMARY KEY,
			message_id TEXT NOT NULL,
			workflow_id TEXT NOT NULL,
			node_id TEXT NOT NULL,
			timestamp TIMESTAMP NOT NULL,
			duration_ms INTEGER,
			data TEXT,
			error TEXT
		)`,
	QueryInitWorkflowVersionsTable: `CREATE TABLE IF NOT EXISTS workflow_versions (
			id TEXT PRIMARY KEY,
			workflow_id TEXT NOT NULL,
			version INTEGER NOT NULL,
			nodes TEXT,
			edges TEXT,
			config TEXT,
			created_at TIMESTAMP NOT NULL,
			created_by TEXT,
			message TEXT,
			UNIQUE(workflow_id, version)
		)`,
	QueryInitOutboxTable: `CREATE TABLE IF NOT EXISTS outbox (
			id TEXT PRIMARY KEY,
			workflow_id TEXT NOT NULL,
			sink_id TEXT NOT NULL,
			payload BLOB,
			metadata TEXT,
			created_at TIMESTAMP NOT NULL,
			attempts INTEGER DEFAULT 0,
			last_error TEXT,
			status TEXT DEFAULT 'pending'
		)`,
	QueryInitWorkspacesTable: `CREATE TABLE IF NOT EXISTS workspaces (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			description TEXT,
			max_workflows INTEGER DEFAULT 0,
			max_cpu REAL DEFAULT 0,
			max_memory REAL DEFAULT 0,
			max_throughput INTEGER DEFAULT 0,
			created_at TIMESTAMP NOT NULL
		)`,
	QueryInitPluginsTable: `CREATE TABLE IF NOT EXISTS plugins (
            id TEXT PRIMARY KEY,
            name TEXT NOT NULL,
            description TEXT,
            author TEXT,
            stars INTEGER,
            category TEXT,
            certified BOOLEAN,
            type TEXT,
            wasm_url TEXT,
            installed BOOLEAN DEFAULT FALSE,
            installed_at TIMESTAMP
        )`,

	QueryUpdateNodeState: "INSERT INTO workflow_node_states (workflow_id, node_id, state) VALUES (?, ?, ?) ON CONFLICT(workflow_id, node_id) DO UPDATE SET state = excluded.state",
	QuerySaveSetting:     "INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",

	QueryListSources:        "SELECT id, name, type, vhost, active, status, worker_id, workspace_id, config, sample, state FROM sources",
	QueryCountSources:       "SELECT COUNT(*) FROM sources",
	QueryCreateSource:       "INSERT INTO sources (id, name, type, vhost, active, status, worker_id, workspace_id, config, sample, state) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
	QueryUpdateSource:       "UPDATE sources SET name = ?, type = ?, vhost = ?, active = ?, status = ?, worker_id = ?, workspace_id = ?, config = ?, sample = ?, state = ? WHERE id = ?",
	QueryUpdateSourceStatus: "UPDATE sources SET status = ? WHERE id = ?",
	QueryUpdateSourceState:  "UPDATE sources SET state = ? WHERE id = ?",
	QueryDeleteSource:       "DELETE FROM sources WHERE id = ?",
	QueryGetSource:          "SELECT id, name, type, vhost, active, status, worker_id, workspace_id, config, sample, state FROM sources WHERE id = ?",

	QueryListSinks:        "SELECT id, name, type, vhost, active, status, worker_id, workspace_id, config FROM sinks",
	QueryCountSinks:       "SELECT COUNT(*) FROM sinks",
	QueryCreateSink:       "INSERT INTO sinks (id, name, type, vhost, active, status, worker_id, workspace_id, config) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
	QueryUpdateSink:       "UPDATE sinks SET name = ?, type = ?, vhost = ?, active = ?, status = ?, worker_id = ?, workspace_id = ?, config = ? WHERE id = ?",
	QueryUpdateSinkStatus: "UPDATE sinks SET status = ? WHERE id = ?",
	QueryDeleteSink:       "DELETE FROM sinks WHERE id = ?",
	QueryGetSink:          "SELECT id, name, type, vhost, active, status, worker_id, workspace_id, config FROM sinks WHERE id = ?",

	QueryListUsers:            "SELECT id, username, full_name, email, role, vhosts, two_factor_enabled FROM users",
	QueryCountUsers:           "SELECT COUNT(*) FROM users",
	QueryCreateUser:           "INSERT INTO users (id, username, password, full_name, email, role, vhosts, two_factor_enabled, two_factor_secret) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
	QueryUpdateUser:           "UPDATE users SET username = ?, password = ?, full_name = ?, email = ?, role = ?, vhosts = ?, two_factor_enabled = ?, two_factor_secret = ? WHERE id = ?",
	QueryUpdateUserNoPassword: "UPDATE users SET username = ?, full_name = ?, email = ?, role = ?, vhosts = ?, two_factor_enabled = ?, two_factor_secret = ? WHERE id = ?",
	QueryDeleteUser:           "DELETE FROM users WHERE id = ?",
	QueryGetUser:              "SELECT id, username, password, full_name, email, role, vhosts, two_factor_enabled, two_factor_secret FROM users WHERE id = ?",
	QueryGetUserByUsername:    "SELECT id, username, password, full_name, email, role, vhosts, two_factor_enabled, two_factor_secret FROM users WHERE username = ?",
	QueryGetUserByEmail:       "SELECT id, username, password, full_name, email, role, vhosts, two_factor_enabled, two_factor_secret FROM users WHERE email = ?",

	QueryListVHosts:  "SELECT id, name, description FROM vhosts",
	QueryCountVHosts: "SELECT COUNT(*) FROM vhosts",
	QueryCreateVHost: "INSERT INTO vhosts (id, name, description) VALUES (?, ?, ?)",
	QueryDeleteVHost: "DELETE FROM vhosts WHERE id = ?",
	QueryGetVHost:    "SELECT id, name, description FROM vhosts WHERE id = ?",

	QueryListWorkflows:        "SELECT id, name, vhost, active, status, worker_id, owner_id, lease_until, nodes, edges, dead_letter_sink_id, prioritize_dlq, max_retries, retry_interval, reconnect_interval, dry_run, schema_type, schema, retention_days, cron, idle_timeout, tier, trace_sample_rate, dlq_threshold, tags, workspace_id, trace_retention, audit_retention, cpu_request, memory_request, throughput_request FROM workflows",
	QueryCountWorkflows:       "SELECT COUNT(*) FROM workflows",
	QueryCreateWorkflow:       "INSERT INTO workflows (id, name, vhost, active, status, worker_id, nodes, edges, dead_letter_sink_id, prioritize_dlq, max_retries, retry_interval, reconnect_interval, dry_run, schema_type, schema, retention_days, cron, idle_timeout, tier, trace_sample_rate, dlq_threshold, tags, workspace_id, trace_retention, audit_retention, cpu_request, memory_request, throughput_request) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
	QueryUpdateWorkflow:       "UPDATE workflows SET name = ?, vhost = ?, active = ?, status = ?, worker_id = ?, nodes = ?, edges = ?, dead_letter_sink_id = ?, prioritize_dlq = ?, max_retries = ?, retry_interval = ?, reconnect_interval = ?, dry_run = ?, schema_type = ?, schema = ?, retention_days = ?, cron = ?, idle_timeout = ?, tier = ?, trace_sample_rate = ?, dlq_threshold = ?, tags = ?, workspace_id = ?, trace_retention = ?, audit_retention = ?, cpu_request = ?, memory_request = ?, throughput_request = ? WHERE id = ?",
	QueryUpdateWorkflowStatus: "UPDATE workflows SET status = ? WHERE id = ?",
	QueryDeleteWorkflow:       "DELETE FROM workflows WHERE id = ?",
	QueryGetWorkflow:          "SELECT id, name, vhost, active, status, worker_id, owner_id, lease_until, nodes, edges, dead_letter_sink_id, prioritize_dlq, max_retries, retry_interval, reconnect_interval, dry_run, schema_type, schema, retention_days, cron, idle_timeout, tier, trace_sample_rate, dlq_threshold, tags, workspace_id, trace_retention, audit_retention, cpu_request, memory_request, throughput_request FROM workflows WHERE id = ?",
	QueryAcquireLease:         "UPDATE workflows SET owner_id = ?, lease_until = ? WHERE id = ? AND (owner_id IS NULL OR lease_until IS NULL OR lease_until < ? OR owner_id = ?)",
	QueryRenewLease:           "UPDATE workflows SET lease_until = ? WHERE id = ? AND owner_id = ? AND lease_until IS NOT NULL AND lease_until >= ?",
	QueryReleaseLease:         "UPDATE workflows SET owner_id = NULL, lease_until = NULL WHERE id = ? AND owner_id = ?",

	QueryListWorkspaces:  "SELECT id, name, description, max_workflows, max_cpu, max_memory, max_throughput, created_at FROM workspaces ORDER BY name ASC",
	QueryCreateWorkspace: "INSERT INTO workspaces (id, name, description, max_workflows, max_cpu, max_memory, max_throughput, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
	QueryDeleteWorkspace: "DELETE FROM workspaces WHERE id = ?",
	QueryGetWorkspace:    "SELECT id, name, description, max_workflows, max_cpu, max_memory, max_throughput, created_at FROM workspaces WHERE id = ?",

	QueryListWorkers:     "SELECT id, name, host, port, description, token, last_seen, cpu_usage, memory_usage FROM workers",
	QueryCountWorkers:    "SELECT COUNT(*) FROM workers",
	QueryCreateWorker:    "INSERT INTO workers (id, name, host, port, description, token, last_seen, cpu_usage, memory_usage) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
	QueryUpdateWorker:    "UPDATE workers SET name = ?, host = ?, port = ?, description = ?, token = ?, last_seen = ?, cpu_usage = ?, memory_usage = ? WHERE id = ?",
	QueryUpdateHeartbeat: "UPDATE workers SET last_seen = ?, cpu_usage = ?, memory_usage = ? WHERE id = ?",
	QueryDeleteWorker:    "DELETE FROM workers WHERE id = ?",
	QueryGetWorker:       "SELECT id, name, host, port, description, token, last_seen, cpu_usage, memory_usage FROM workers WHERE id = ?",

	QueryListLogs:   "SELECT id, timestamp, level, message, action, source_id, sink_id, workflow_id, user_id, username, data FROM logs",
	QueryCountLogs:  "SELECT COUNT(*) FROM logs",
	QueryCreateLog:  "INSERT INTO logs (id, timestamp, level, message, action, source_id, sink_id, workflow_id, user_id, username, data) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
	QueryDeleteLogs: "DELETE FROM logs",

	QueryGetSetting: "SELECT value FROM settings WHERE key = ?",

	QueryCreateAuditLog:     "INSERT INTO audit_logs (id, timestamp, user_id, username, action, entity_type, entity_id, payload, ip) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
	QueryListAuditLogs:      "SELECT id, timestamp, user_id, username, action, entity_type, entity_id, payload, ip FROM audit_logs",
	QueryCountAuditLogs:     "SELECT COUNT(*) FROM audit_logs",
	QueryPurgeAuditLogs:     "DELETE FROM audit_logs WHERE timestamp < ?",
	QueryPurgeMessageTraces: "DELETE FROM message_trace_steps WHERE timestamp < ?",

	QueryCreateWebhookRequest:  "INSERT INTO webhook_requests (id, timestamp, path, method, headers, body) VALUES (?, ?, ?, ?, ?, ?)",
	QueryListWebhookRequests:   "SELECT id, timestamp, path, method, headers, body FROM webhook_requests",
	QueryCountWebhookRequests:  "SELECT COUNT(*) FROM webhook_requests",
	QueryGetWebhookRequest:     "SELECT id, timestamp, path, method, headers, body FROM webhook_requests WHERE id = ?",
	QueryDeleteWebhookRequests: "DELETE FROM webhook_requests",

	QueryCreateFormSubmission:       "INSERT INTO form_submissions (id, timestamp, path, data, status) VALUES (?, ?, ?, ?, ?)",
	QueryListFormSubmissions:        "SELECT id, timestamp, path, data, status FROM form_submissions",
	QueryCountFormSubmissions:       "SELECT COUNT(*) FROM form_submissions",
	QueryGetFormSubmission:          "SELECT id, timestamp, path, data, status FROM form_submissions WHERE id = ?",
	QueryUpdateFormSubmissionStatus: "UPDATE form_submissions SET status = ? WHERE id = ?",
	QueryDeleteFormSubmissions:      "DELETE FROM form_submissions",

	QueryListSchemas:     "SELECT id, name, version, type, content, created_at FROM schemas WHERE name = ? ORDER BY version DESC",
	QueryListAllSchemas:  "SELECT id, name, version, type, content, created_at FROM schemas WHERE (name, version) IN (SELECT name, MAX(version) FROM schemas GROUP BY name) ORDER BY name ASC",
	QueryGetSchema:       "SELECT id, name, version, type, content, created_at FROM schemas WHERE name = ? AND version = ?",
	QueryGetLatestSchema: "SELECT id, name, version, type, content, created_at FROM schemas WHERE name = ? ORDER BY version DESC LIMIT 1",
	QueryCreateSchema:    "INSERT INTO schemas (id, name, version, type, content, created_at) VALUES (?, ?, ?, ?, ?, ?)",

	QueryRecordTraceStep:   "INSERT INTO message_trace_steps (id, message_id, workflow_id, node_id, timestamp, duration_ms, data, error) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
	QueryGetMessageTrace:   "SELECT node_id, timestamp, duration_ms, data, error FROM message_trace_steps WHERE workflow_id = ? AND message_id = ? ORDER BY timestamp ASC",
	QueryListMessageTraces: "SELECT DISTINCT message_id, MIN(timestamp) as start_time FROM message_trace_steps WHERE workflow_id = ? GROUP BY message_id ORDER BY start_time DESC LIMIT ?",

	QueryCreateWorkflowVersion: "INSERT INTO workflow_versions (id, workflow_id, version, nodes, edges, config, created_at, created_by, message) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
	QueryListWorkflowVersions:  "SELECT id, workflow_id, version, nodes, edges, config, created_at, created_by, message FROM workflow_versions WHERE workflow_id = ? ORDER BY version DESC",
	QueryGetWorkflowVersion:    "SELECT id, workflow_id, version, nodes, edges, config, created_at, created_by, message FROM workflow_versions WHERE workflow_id = ? AND version = ?",

	QueryCreateOutboxItem: "INSERT INTO outbox (id, workflow_id, sink_id, payload, metadata, created_at, attempts, last_error, status) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
	QueryListOutboxItems:  "SELECT id, workflow_id, sink_id, payload, metadata, created_at, attempts, last_error, status FROM outbox WHERE status = ? ORDER BY created_at ASC LIMIT ?",
	QueryDeleteOutboxItem: "DELETE FROM outbox WHERE id = ?",
	QueryUpdateOutboxItem: "UPDATE outbox SET attempts = ?, last_error = ?, status = ? WHERE id = ?",
	QueryListPlugins:      "SELECT id, name, description, author, stars, category, certified, type, wasm_url, installed, installed_at FROM plugins",
	QueryGetPlugin:        "SELECT id, name, description, author, stars, category, certified, type, wasm_url, installed, installed_at FROM plugins WHERE id = ?",
	QueryCreatePlugin:     "INSERT INTO plugins (id, name, description, author, stars, category, certified, type, wasm_url, installed, installed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
	QueryUpdatePlugin:     "UPDATE plugins SET name = ?, description = ?, author = ?, stars = ?, category = ?, certified = ?, type = ?, wasm_url = ?, installed = ?, installed_at = ? WHERE id = ?",
	QueryInstallPlugin:    "UPDATE plugins SET installed = TRUE, installed_at = ? WHERE id = ?",
	QueryUninstallPlugin:  "UPDATE plugins SET installed = FALSE, installed_at = NULL WHERE id = ?",
}

var driverOverrides = map[string]map[string]string{
	"mysql": {
		QueryUpdateNodeState: "INSERT INTO workflow_node_states (workflow_id, node_id, state) VALUES (?, ?, ?) ON DUPLICATE KEY UPDATE state = VALUES(state)",
		QuerySaveSetting:     "INSERT INTO settings (`key`, value) VALUES (?, ?) ON DUPLICATE KEY UPDATE value = VALUES(value)",
	},
	"mariadb": {
		QueryUpdateNodeState: "INSERT INTO workflow_node_states (workflow_id, node_id, state) VALUES (?, ?, ?) ON DUPLICATE KEY UPDATE state = VALUES(state)",
		QuerySaveSetting:     "INSERT INTO settings (`key`, value) VALUES (?, ?) ON DUPLICATE KEY UPDATE value = VALUES(value)",
	},
	"pgx": {
		QueryUpdateNodeState: "INSERT INTO workflow_node_states (workflow_id, node_id, state) VALUES ($1, $2, $3) ON CONFLICT(workflow_id, node_id) DO UPDATE SET state = excluded.state",
		QuerySaveSetting:     "INSERT INTO settings (key, value) VALUES ($1, $2) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
	},
	"sqlserver": {
		QuerySaveSetting: "MERGE settings WITH (HOLDLOCK) AS t USING (SELECT @p1 AS [key], @p2 AS value) AS s ON t.[key] = s.[key] WHEN MATCHED THEN UPDATE SET value = s.value WHEN NOT MATCHED THEN INSERT([key], value) VALUES(s.[key], s.value);",
	},
}
