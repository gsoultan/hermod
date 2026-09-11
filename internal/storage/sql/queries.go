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
	QueryInitApprovalsTable          = "InitApprovalsTable"

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
	QueryUpdateVHost = "UpdateVHost"
	QueryDeleteVHost = "DeleteVHost"
	QueryGetVHost    = "GetVHost"

	// Workflows
	QueryListWorkflows        = "ListWorkflows"
	QueryCountWorkflows       = "CountWorkflows"
	QueryCreateWorkflow       = "CreateWorkflow"
	QueryUpdateWorkflow       = "UpdateWorkflow"
	QueryUpdateWorkflowStatus = "UpdateWorkflowStatus"
	QueryUpdateWorkflowStats  = "UpdateWorkflowStats"
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
	QueryRecordTraceStep = "RecordTraceStep"
	QueryGetMessageTrace = "GetMessageTrace"

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

	// Approvals
	QueryListApprovals        = "ListApprovals"
	QueryCountApprovals       = "CountApprovals"
	QueryCreateApproval       = "CreateApproval"
	QueryGetApproval          = "GetApproval"
	QueryUpdateApprovalStatus = "UpdateApprovalStatus"
	QueryDeleteApproval       = "DeleteApproval"

	QueryInitSuspendedMessagesTable = "InitSuspendedMessagesTable"
	QueryCreateSuspendedMessage     = "CreateSuspendedMessage"
	QueryListSuspendedMessages      = "ListSuspendedMessages"
	QueryDeleteSuspendedMessage     = "DeleteSuspendedMessage"

	QueryInitMessageTracesTable   = "InitMessageTracesTable"
	QueryUpsertMessageTrace       = "UpsertMessageTrace"
	QueryListMessageTracesKeyset  = "ListMessageTracesKeyset"
	QueryListMessageTracesOffset  = "ListMessageTracesOffset"
	QueryPurgeMessageTraceParents = "PurgeMessageTraceParents"
	QueryRecordTraceStepLegacyID  = "RecordTraceStepLegacyID"

	QueryInitDashboardHistoryTable = "InitDashboardHistoryTable"
	QueryRecordDashboardSample     = "RecordDashboardSample"
	QueryGetDashboardHistory       = "GetDashboardHistory"
	QueryPurgeDashboardHistory     = "PurgeDashboardHistory"
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
            throughput_request INTEGER DEFAULT 0,
            total_processed BIGINT DEFAULT 0,
            total_errors BIGINT DEFAULT 0,
            total_lag BIGINT DEFAULT 0
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
	// No surrogate key and no before_data.
	//
	// The id was a UUID written on every step and selected by nothing — the
	// same write-only column dashboard_history had. before_data held the
	// payload entering a node, which is by definition the after_data of the
	// node before it, so the whole payload chain was stored twice; it is
	// reconstructed on read instead. Measured on PostgreSQL 17 with realistic
	// incompressible payloads, 250k rows: 262 MB -> 123 MB.
	//
	// Rows are never addressed individually — reads are
	// (workflow_id, message_id) and deletes are a timestamp range — so nothing
	// is lost by having no key. See the dashboard_history DDL for the one
	// consequence (PostgreSQL logical replication and replica identity).
	QueryInitMessageTraceStepsTable: `CREATE TABLE IF NOT EXISTS message_trace_steps (
			message_id TEXT NOT NULL,
			workflow_id TEXT NOT NULL,
			node_id TEXT NOT NULL,
			timestamp TIMESTAMP NOT NULL,
			duration_ms INTEGER,
			after_data TEXT,
			error TEXT
		)`,

	// One row per traced message, written alongside the steps.
	//
	// Listing traces used to be
	//   SELECT DISTINCT message_id, MIN(timestamp) ... GROUP BY message_id
	// over the step table, which no index can satisfy: every step the workflow
	// ever produced had to be aggregated before the first 25 rows could be
	// returned. Measured on PostgreSQL 17 at 250k steps that was a sequential
	// scan, ~390 MB of I/O and 58.5 ms, growing linearly with the table. The
	// same page off this table is an index scan at 0.071 ms, and stays there.
	QueryInitMessageTracesTable: `CREATE TABLE IF NOT EXISTS message_traces (
			workflow_id TEXT NOT NULL,
			message_id TEXT NOT NULL,
			started_at TIMESTAMP NOT NULL,
			last_step_at TIMESTAMP NOT NULL,
			duration_ms BIGINT NOT NULL DEFAULT 0,
			step_count INTEGER NOT NULL DEFAULT 0,
			error_count INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (workflow_id, message_id)
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
	QueryInitApprovalsTable: `CREATE TABLE IF NOT EXISTS approvals (
            id TEXT PRIMARY KEY,
            workflow_id TEXT NOT NULL,
            node_id TEXT NOT NULL,
            message_id TEXT NOT NULL,
            payload BLOB,
            metadata TEXT,
            data TEXT,
            form_definition TEXT,
            form_data TEXT,
            status TEXT DEFAULT 'pending',
            created_at TIMESTAMP NOT NULL,
            processed_at TIMESTAMP,
            processed_by TEXT,
            notes TEXT
        )`,
	QueryInitSuspendedMessagesTable: `CREATE TABLE IF NOT EXISTS suspended_messages (
			id TEXT PRIMARY KEY,
			workflow_id TEXT NOT NULL,
			node_id TEXT NOT NULL,
			payload BLOB,
			metadata TEXT,
			data TEXT,
			resume_at TIMESTAMP NOT NULL,
			created_at TIMESTAMP NOT NULL
		)`,
	// One row per sampling interval per vhost. vhost is NOT NULL with a ''
	// default rather than nullable: '' is the global aggregate here, and a
	// NULL would make `WHERE vhost = ''` skip exactly the rows the unfiltered
	// dashboard asks for.
	// Deliberately no surrogate primary key. Rows here are never addressed
	// individually — every read is a range scan over (vhost, timestamp) and
	// every delete is a range sweep — so an id column would be written on
	// every tick and selected by nothing. Measured on a week of samples for
	// one vhost in SQLite, a UUID primary key was 9.6 MB of a 21 MB table:
	// 46% of the disk, for an identifier no query uses.
	//
	// (vhost, timestamp) is not promoted to a primary key in its place: the
	// timestamp is truncated to the second, and nothing guarantees one writer
	// — there is no leader election, so two control-plane nodes sharing a
	// metadata database both sample — so a natural key would turn a duplicate
	// into a failed insert. The one consequence of having no key at all is
	// that PostgreSQL logical replication refuses to publish DELETEs from a
	// table with no replica identity; an operator who adds this table to a
	// publication needs REPLICA IDENTITY FULL, or to leave it out, which is
	// the better answer for a metrics table nobody replicates.
	QueryInitDashboardHistoryTable: `CREATE TABLE IF NOT EXISTS dashboard_history (
			vhost TEXT NOT NULL DEFAULT '',
			timestamp TIMESTAMP NOT NULL,
			throughput REAL NOT NULL DEFAULT 0,
			total_processed BIGINT NOT NULL DEFAULT 0,
			total_errors BIGINT NOT NULL DEFAULT 0,
			total_lag BIGINT NOT NULL DEFAULT 0,
			error_rate REAL NOT NULL DEFAULT 0,
			avg_latency_ms REAL NOT NULL DEFAULT 0,
			active_workflows INTEGER NOT NULL DEFAULT 0,
			active_workers INTEGER NOT NULL DEFAULT 0
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
	QueryUpdateVHost: "UPDATE vhosts SET name = ?, description = ? WHERE id = ?",
	QueryDeleteVHost: "DELETE FROM vhosts WHERE id = ?",
	QueryGetVHost:    "SELECT id, name, description FROM vhosts WHERE id = ?",

	QueryListWorkflows:        "SELECT id, name, vhost, active, status, worker_id, owner_id, lease_until, nodes, edges, dead_letter_sink_id, prioritize_dlq, max_retries, retry_interval, reconnect_interval, dry_run, schema_type, schema, retention_days, cron, idle_timeout, tier, trace_sample_rate, dlq_threshold, tags, workspace_id, trace_retention, audit_retention, cpu_request, memory_request, throughput_request, total_processed, total_errors, total_lag FROM workflows",
	QueryCountWorkflows:       "SELECT COUNT(*) FROM workflows",
	QueryCreateWorkflow:       "INSERT INTO workflows (id, name, vhost, active, status, worker_id, nodes, edges, dead_letter_sink_id, prioritize_dlq, max_retries, retry_interval, reconnect_interval, dry_run, schema_type, schema, retention_days, cron, idle_timeout, tier, trace_sample_rate, dlq_threshold, tags, workspace_id, trace_retention, audit_retention, cpu_request, memory_request, throughput_request, total_processed, total_errors, total_lag) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
	QueryUpdateWorkflow:       "UPDATE workflows SET name = ?, vhost = ?, active = ?, status = ?, worker_id = ?, nodes = ?, edges = ?, dead_letter_sink_id = ?, prioritize_dlq = ?, max_retries = ?, retry_interval = ?, reconnect_interval = ?, dry_run = ?, schema_type = ?, schema = ?, retention_days = ?, cron = ?, idle_timeout = ?, tier = ?, trace_sample_rate = ?, dlq_threshold = ?, tags = ?, workspace_id = ?, trace_retention = ?, audit_retention = ?, cpu_request = ?, memory_request = ?, throughput_request = ?, total_processed = ?, total_errors = ?, total_lag = ? WHERE id = ?",
	QueryUpdateWorkflowStatus: "UPDATE workflows SET status = ? WHERE id = ?",
	QueryUpdateWorkflowStats:  "UPDATE workflows SET total_processed = ?, total_errors = ?, total_lag = ? WHERE id = ?",
	QueryDeleteWorkflow:       "DELETE FROM workflows WHERE id = ?",
	QueryGetWorkflow:          "SELECT id, name, vhost, active, status, worker_id, owner_id, lease_until, nodes, edges, dead_letter_sink_id, prioritize_dlq, max_retries, retry_interval, reconnect_interval, dry_run, schema_type, schema, retention_days, cron, idle_timeout, tier, trace_sample_rate, dlq_threshold, tags, workspace_id, trace_retention, audit_retention, cpu_request, memory_request, throughput_request, total_processed, total_errors, total_lag FROM workflows WHERE id = ?",
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

	QueryRecordTraceStep: "INSERT INTO message_trace_steps (message_id, workflow_id, node_id, timestamp, duration_ms, after_data, error) VALUES (?, ?, ?, ?, ?, ?, ?)",
	// Used only where the id column survives because the database predates the
	// narrowing and could not be altered (SQLite cannot drop a primary key).
	// It is NOT NULL there, so omitting it would fail every trace write.
	QueryRecordTraceStepLegacyID: "INSERT INTO message_trace_steps (id, message_id, workflow_id, node_id, timestamp, duration_ms, after_data, error) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",

	QueryUpsertMessageTrace: "INSERT INTO message_traces (workflow_id, message_id, started_at, last_step_at, duration_ms, step_count, error_count) VALUES (?, ?, ?, ?, ?, 1, ?) ON CONFLICT(workflow_id, message_id) DO UPDATE SET last_step_at = excluded.last_step_at, duration_ms = message_traces.duration_ms + excluded.duration_ms, step_count = message_traces.step_count + 1, error_count = message_traces.error_count + excluded.error_count",

	// Newest-first with a keyset cursor; the caller passes the CreatedAt of the
	// last row it saw, so page 200 costs what page 1 does.
	QueryListMessageTracesKeyset: "SELECT message_id, started_at, duration_ms, step_count, error_count FROM message_traces WHERE workflow_id = ? AND started_at < ? ORDER BY started_at DESC LIMIT ?",
	QueryListMessageTracesOffset: "SELECT message_id, started_at, duration_ms, step_count, error_count FROM message_traces WHERE workflow_id = ? ORDER BY started_at DESC LIMIT ? OFFSET ?",

	QueryPurgeMessageTraceParents: "DELETE FROM message_traces WHERE started_at < ?",
	QueryGetMessageTrace:          "SELECT node_id, timestamp, duration_ms, after_data, error FROM message_trace_steps WHERE workflow_id = ? AND message_id = ? ORDER BY timestamp ASC",

	QueryCreateWorkflowVersion: "INSERT INTO workflow_versions (id, workflow_id, version, nodes, edges, config, created_at, created_by, message) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
	QueryListWorkflowVersions:  "SELECT id, workflow_id, version, created_at, created_by, message FROM workflow_versions WHERE workflow_id = ? ORDER BY version DESC",
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

	// Approvals
	QueryListApprovals:        "SELECT id, workflow_id, node_id, message_id, payload, metadata, data, form_definition, form_data, status, created_at, processed_at, processed_by, notes FROM approvals",
	QueryCountApprovals:       "SELECT COUNT(*) FROM approvals",
	QueryCreateApproval:       "INSERT INTO approvals (id, workflow_id, node_id, message_id, payload, metadata, data, form_definition, status, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
	QueryGetApproval:          "SELECT id, workflow_id, node_id, message_id, payload, metadata, data, form_definition, form_data, status, created_at, processed_at, processed_by, notes FROM approvals WHERE id = ?",
	QueryUpdateApprovalStatus: "UPDATE approvals SET status = ?, processed_at = ?, processed_by = ?, notes = ?, form_data = ? WHERE id = ?",
	// Suspended Messages
	QueryCreateSuspendedMessage: "INSERT INTO suspended_messages (id, workflow_id, node_id, payload, metadata, data, resume_at, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
	QueryListSuspendedMessages:  "SELECT id, workflow_id, node_id, payload, metadata, data, resume_at, created_at FROM suspended_messages WHERE resume_at <= ?",
	QueryDeleteSuspendedMessage: "DELETE FROM suspended_messages WHERE id = ?",

	QueryRecordDashboardSample: "INSERT INTO dashboard_history (vhost, timestamp, throughput, total_processed, total_errors, total_lag, error_rate, avg_latency_ms, active_workflows, active_workers) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
	// Ordered newest-first so LIMIT keeps the recent end of the series; the
	// caller reverses into the oldest-first order the chart plots.
	QueryGetDashboardHistory:   "SELECT timestamp, vhost, throughput, total_processed, total_errors, total_lag, error_rate, avg_latency_ms, active_workflows, active_workers FROM dashboard_history WHERE vhost = ? AND timestamp >= ? ORDER BY timestamp DESC LIMIT ?",
	QueryPurgeDashboardHistory: "DELETE FROM dashboard_history WHERE timestamp < ?",
}

var driverOverrides = map[string]map[string]string{
	"mysql": {
		QueryUpsertMessageTrace: "INSERT INTO message_traces (workflow_id, message_id, started_at, last_step_at, duration_ms, step_count, error_count) VALUES (?, ?, ?, ?, ?, 1, ?) ON DUPLICATE KEY UPDATE last_step_at = VALUES(last_step_at), duration_ms = duration_ms + VALUES(duration_ms), step_count = step_count + 1, error_count = error_count + VALUES(error_count)",
		QueryUpdateNodeState:    "INSERT INTO workflow_node_states (workflow_id, node_id, state) VALUES (?, ?, ?) ON DUPLICATE KEY UPDATE state = VALUES(state)",
		QuerySaveSetting:        "INSERT INTO settings (`key`, value) VALUES (?, ?) ON DUPLICATE KEY UPDATE value = VALUES(value)",
	},
	"mariadb": {
		QueryUpsertMessageTrace: "INSERT INTO message_traces (workflow_id, message_id, started_at, last_step_at, duration_ms, step_count, error_count) VALUES (?, ?, ?, ?, ?, 1, ?) ON DUPLICATE KEY UPDATE last_step_at = VALUES(last_step_at), duration_ms = duration_ms + VALUES(duration_ms), step_count = step_count + 1, error_count = error_count + VALUES(error_count)",
		QueryUpdateNodeState:    "INSERT INTO workflow_node_states (workflow_id, node_id, state) VALUES (?, ?, ?) ON DUPLICATE KEY UPDATE state = VALUES(state)",
		QuerySaveSetting:        "INSERT INTO settings (`key`, value) VALUES (?, ?) ON DUPLICATE KEY UPDATE value = VALUES(value)",
	},
	"pgx": {
		QueryUpdateNodeState: "INSERT INTO workflow_node_states (workflow_id, node_id, state) VALUES ($1, $2, $3) ON CONFLICT(workflow_id, node_id) DO UPDATE SET state = excluded.state",
		QuerySaveSetting:     "INSERT INTO settings (key, value) VALUES ($1, $2) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
	},
	"sqlserver": {
		QueryUpsertMessageTrace: "MERGE message_traces WITH (HOLDLOCK) AS t USING (SELECT @p1 AS workflow_id, @p2 AS message_id, @p3 AS started_at, @p4 AS last_step_at, @p5 AS duration_ms, @p6 AS error_count) AS s ON t.workflow_id = s.workflow_id AND t.message_id = s.message_id WHEN MATCHED THEN UPDATE SET last_step_at = s.last_step_at, duration_ms = t.duration_ms + s.duration_ms, step_count = t.step_count + 1, error_count = t.error_count + s.error_count WHEN NOT MATCHED THEN INSERT (workflow_id, message_id, started_at, last_step_at, duration_ms, step_count, error_count) VALUES (s.workflow_id, s.message_id, s.started_at, s.last_step_at, s.duration_ms, 1, s.error_count);",
		QuerySaveSetting:        "MERGE settings WITH (HOLDLOCK) AS t USING (SELECT @p1 AS [key], @p2 AS value) AS s ON t.[key] = s.[key] WHEN MATCHED THEN UPDATE SET value = s.value WHEN NOT MATCHED THEN INSERT([key], value) VALUES(s.[key], s.value);",
	},
}
