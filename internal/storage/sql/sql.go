package sql

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/user/hermod"
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/crypto"
	_ "modernc.org/sqlite"
)

var sensitiveKeys = map[string]bool{
	"password":          true,
	"connection_string": true,
	"uri":               true,
	"token":             true,
	"secret":            true,
	"key":               true,
	"access_key":        true,
	"secret_key":        true,
}

func encryptConfig(config map[string]string) map[string]string {
	encrypted := make(map[string]string)
	for k, v := range config {
		if sensitiveKeys[strings.ToLower(k)] && v != "" {
			enc, err := crypto.Encrypt(v)
			if err == nil {
				encrypted[k] = "enc:" + enc
				continue
			}
		}
		encrypted[k] = v
	}
	return encrypted
}

func decryptConfig(config map[string]string) map[string]string {
	decrypted := make(map[string]string)
	for k, v := range config {
		if strings.HasPrefix(v, "enc:") {
			dec, err := crypto.Decrypt(v[4:])
			if err == nil {
				decrypted[k] = dec
				continue
			}
		}
		decrypted[k] = v
	}
	return decrypted
}

type sqlStorage struct {
	db      *sql.DB
	driver  string
	queries *queryRegistry
}

func NewSQLStorage(db *sql.DB, driver string) storage.Storage {
	return &sqlStorage{
		db:      db,
		driver:  driver,
		queries: newQueryRegistry(driver),
	}
}

// prepareQuery rewrites parameter placeholders and types to match the current driver.
func (s *sqlStorage) prepareQuery(query string) string {
	q := s.preparePlaceholders(query)
	if s.driver == "pgx" || s.driver == "postgres" {
		q = strings.ReplaceAll(q, "BLOB", "BYTEA")
		q = strings.ReplaceAll(q, "REAL", "DOUBLE PRECISION")
	}
	return q
}

// preparePlaceholders rewrites parameter placeholders to match the current driver.
//
// Default (sqlite, mysql, mariadb) uses '?' and is passed through unchanged.
// Postgres (pgx) requires $1, $2, ...
// SQL Server (sqlserver) commonly uses @p1, @p2, ...
// This helper keeps SQL definitions central and simple while remaining portable.
func (s *sqlStorage) preparePlaceholders(query string) string {
	switch s.driver {
	case "pgx", "postgres":
		// Replace each '?' with $n
		var b strings.Builder
		b.Grow(len(query) + 8) // small headroom
		idx := 1
		for i := 0; i < len(query); i++ {
			if query[i] == '?' {
				b.WriteByte('$')
				b.WriteString(strconv.Itoa(idx))
				idx++
				continue
			}
			b.WriteByte(query[i])
		}
		return b.String()
	case "sqlserver":
		// Replace each '?' with @pN
		var b strings.Builder
		b.Grow(len(query) + 8)
		idx := 1
		for i := 0; i < len(query); i++ {
			if query[i] == '?' {
				b.WriteString("@p")
				b.WriteString(strconv.Itoa(idx))
				idx++
				continue
			}
			b.WriteByte(query[i])
		}
		return b.String()
	default:
		return query
	}
}

// exec wraps db.ExecContext with driver-specific placeholder and type preparation.
func (s *sqlStorage) exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	q := s.prepareQuery(query)
	return s.db.ExecContext(ctx, q, args...)
}

// query wraps db.QueryContext with driver-specific placeholder and type preparation.
func (s *sqlStorage) query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	q := s.prepareQuery(query)
	return s.db.QueryContext(ctx, q, args...)
}

// queryRow wraps db.QueryRowContext with driver-specific placeholder and type preparation.
func (s *sqlStorage) queryRow(ctx context.Context, query string, args ...any) *sql.Row {
	q := s.prepareQuery(query)
	return s.db.QueryRowContext(ctx, q, args...)
}

func (s *sqlStorage) Init(ctx context.Context) error {
	// SQLite specific optimizations
	if s.driver == "sqlite" {
		_, _ = s.db.ExecContext(ctx, "PRAGMA journal_mode=WAL")
		// Respect DSN-configured busy_timeout by default.
		// Only override if HERMOD_SQLITE_BUSY_TIMEOUT_MS is explicitly set.
		if v := os.Getenv("HERMOD_SQLITE_BUSY_TIMEOUT_MS"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n >= 0 {
				_, _ = s.db.ExecContext(ctx, fmt.Sprintf("PRAGMA busy_timeout=%d", n))
			}
		}
		_, _ = s.db.ExecContext(ctx, "PRAGMA synchronous=NORMAL")
		_, _ = s.db.ExecContext(ctx, "PRAGMA foreign_keys=ON")
	}

	// 1. Initialize tables if they do not exist
	initQueries := []string{
		s.queries.get(QueryInitSourcesTable),
		s.queries.get(QueryInitSinksTable),
		s.queries.get(QueryInitWorkflowsTable),
		s.queries.get(QueryInitWorkflowNodeStatesTable),
		s.queries.get(QueryInitLogsTable),
		s.queries.get(QueryInitWebhookRequestsTable),
		s.queries.get(QueryInitFormSubmissionsTable),
		s.queries.get(QueryInitUsersTable),
		s.queries.get(QueryInitVHostsTable),
		s.queries.get(QueryInitWorkersTable),
		s.queries.get(QueryInitApprovalsTable),
	}

	for _, q := range initQueries {
		if _, err := s.db.ExecContext(ctx, s.prepareQuery(q)); err != nil {
			return fmt.Errorf("failed to init table: %w", err)
		}
	}

	// 2. Perform automatic schema migration to add any missing columns.
	// This ensures that existing databases are kept in sync with code-level schema changes.
	s.autoMigrate(ctx)

	// 3. Initialize indexes and other tables
	indexQueries := []string{
		// Logs
		"CREATE INDEX IF NOT EXISTS idx_logs_workflow_id ON logs(workflow_id)",
		"CREATE INDEX IF NOT EXISTS idx_logs_source_id ON logs(source_id)",
		"CREATE INDEX IF NOT EXISTS idx_logs_sink_id ON logs(sink_id)",
		"CREATE INDEX IF NOT EXISTS idx_logs_level ON logs(level)",
		"CREATE INDEX IF NOT EXISTS idx_logs_action ON logs(action)",
		"CREATE INDEX IF NOT EXISTS idx_logs_user_id ON logs(user_id)",
		"CREATE INDEX IF NOT EXISTS idx_logs_username ON logs(username)",
		"CREATE INDEX IF NOT EXISTS idx_logs_timestamp ON logs(timestamp)",
		// Composite indexes to accelerate common filtered sorts by timestamp
		"CREATE INDEX IF NOT EXISTS idx_logs_workflow_id_ts ON logs(workflow_id, timestamp DESC)",
		"CREATE INDEX IF NOT EXISTS idx_logs_source_id_ts ON logs(source_id, timestamp DESC)",
		"CREATE INDEX IF NOT EXISTS idx_logs_sink_id_ts ON logs(sink_id, timestamp DESC)",
		"CREATE INDEX IF NOT EXISTS idx_logs_level_ts ON logs(level, timestamp DESC)",
		"CREATE INDEX IF NOT EXISTS idx_logs_action_ts ON logs(action, timestamp DESC)",
		// Workflows
		"CREATE INDEX IF NOT EXISTS idx_workflows_owner ON workflows(owner_id)",
		"CREATE INDEX IF NOT EXISTS idx_workflows_lease_until ON workflows(lease_until)",
		"CREATE INDEX IF NOT EXISTS idx_workflows_vhost ON workflows(vhost)",
		// Sources/Sinks
		"CREATE INDEX IF NOT EXISTS idx_sources_vhost ON sources(vhost)",
		"CREATE INDEX IF NOT EXISTS idx_sinks_vhost ON sinks(vhost)",
		// Helpful for equality lookups by name (search uses LIKE and may not benefit)
		"CREATE INDEX IF NOT EXISTS idx_sources_name ON sources(name)",
		"CREATE INDEX IF NOT EXISTS idx_sinks_name ON sinks(name)",
	}

	for _, q := range indexQueries {
		// Ignore errors as the index might already exist
		_, _ = s.db.ExecContext(ctx, s.prepareQuery(q))
	}

	_, err := s.db.ExecContext(ctx, s.prepareQuery(s.queries.get(QueryInitSettingsTable)))
	if err != nil {
		return fmt.Errorf("failed to create settings table: %w", err)
	}

	_, err = s.db.ExecContext(ctx, s.prepareQuery(s.queries.get(QueryInitAuditLogsTable)))
	if err != nil {
		return fmt.Errorf("failed to create audit_logs table: %w", err)
	}

	_, _ = s.db.ExecContext(ctx, s.prepareQuery("CREATE INDEX IF NOT EXISTS idx_audit_ts ON audit_logs(timestamp DESC)"))
	_, _ = s.db.ExecContext(ctx, s.prepareQuery("CREATE INDEX IF NOT EXISTS idx_audit_user ON audit_logs(user_id)"))
	_, _ = s.db.ExecContext(ctx, s.prepareQuery("CREATE INDEX IF NOT EXISTS idx_audit_entity ON audit_logs(entity_type, entity_id)"))

	_, err = s.db.ExecContext(ctx, s.prepareQuery(s.queries.get(QueryInitSchemasTable)))
	if err != nil {
		return fmt.Errorf("failed to create schemas table: %w", err)
	}

	_, err = s.db.ExecContext(ctx, s.prepareQuery(s.queries.get(QueryInitMessageTraceStepsTable)))
	if err != nil {
		return fmt.Errorf("failed to create message_trace_steps table: %w", err)
	}

	_, _ = s.db.ExecContext(ctx, s.prepareQuery("CREATE INDEX IF NOT EXISTS idx_trace_msg ON message_trace_steps(workflow_id, message_id)"))

	_, err = s.db.ExecContext(ctx, s.prepareQuery(s.queries.get(QueryInitWorkflowVersionsTable)))
	if err != nil {
		return fmt.Errorf("failed to create workflow_versions table: %w", err)
	}

	_, _ = s.db.ExecContext(ctx, s.prepareQuery("CREATE INDEX IF NOT EXISTS idx_workflow_versions_id ON workflow_versions(workflow_id, version)"))

	_, err = s.db.ExecContext(ctx, s.prepareQuery(s.queries.get(QueryInitOutboxTable)))
	if err != nil {
		return fmt.Errorf("failed to create outbox table: %w", err)
	}

	_, _ = s.db.ExecContext(ctx, s.prepareQuery("CREATE INDEX IF NOT EXISTS idx_outbox_status ON outbox(status, created_at)"))

	_, err = s.db.ExecContext(ctx, s.prepareQuery(s.queries.get(QueryInitWorkspacesTable)))
	if err != nil {
		return fmt.Errorf("failed to create workspaces table: %w", err)
	}

	_, err = s.db.ExecContext(ctx, s.prepareQuery(s.queries.get(QueryInitPluginsTable)))
	if err != nil {
		return fmt.Errorf("failed to create plugins table: %w", err)
	}

	s.seedPlugins(ctx)

	return nil
}

func (s *sqlStorage) seedPlugins(ctx context.Context) {
	plugins, _ := s.ListPlugins(ctx)
	if len(plugins) > 0 {
		return
	}

	initialPlugins := []storage.Plugin{
		{
			ID:          "openai-pii-filter",
			Name:        "OpenAI PII Filter",
			Description: "Anonymize sensitive data using OpenAI's GPT-4 before it leaves your infrastructure.",
			Author:      "Hermod Core",
			Stars:       128,
			Category:    "Security",
			Certified:   true,
			Type:        "Transformer",
			WasmURL:     "https://github.com/user/hermod-plugins/raw/main/openai-pii-filter.wasm",
		},
		{
			ID:          "slack-connector",
			Name:        "Slack Connector",
			Description: "Send alerts and notifications to Slack channels with advanced formatting.",
			Author:      "Hermod Core",
			Stars:       89,
			Category:    "Connectors",
			Certified:   true,
			Type:        "Connector",
		},
		{
			ID:          "xml-to-json",
			Name:        "XML to JSON",
			Description: "High-performance WASM-based transformer to convert legacy XML payloads to JSON.",
			Author:      "Community",
			Stars:       45,
			Category:    "Transformation",
			Certified:   false,
			Type:        "WASM",
			WasmURL:     "https://github.com/user/hermod-plugins/raw/main/xml-to-json.wasm",
		},
	}

	for _, p := range initialPlugins {
		_ = s.execWithRetry(ctx, func() error {
			_, e := s.exec(ctx, s.queries.get(QueryCreatePlugin),
				p.ID, p.Name, p.Description, p.Author, p.Stars, p.Category, p.Certified, p.Type, p.WasmURL, p.Installed, p.InstalledAt)
			return e
		})
	}
}

// autoMigrate automatically adds missing columns to tables based on the CREATE TABLE definitions in commonQueries.
// This ensures that existing databases are kept in sync with the current schema without manual migration steps
// for every new field.
func (s *sqlStorage) autoMigrate(ctx context.Context) {
	for _, query := range commonQueries {
		q := strings.TrimSpace(query)
		if !strings.HasPrefix(strings.ToUpper(q), "CREATE TABLE") {
			continue
		}

		// Extract table name
		// Match "CREATE TABLE [IF NOT EXISTS] tableName ("
		q = strings.ReplaceAll(q, "\n", " ")
		q = strings.ReplaceAll(q, "\t", " ")

		openParenIdx := strings.Index(q, "(")
		if openParenIdx == -1 {
			continue
		}
		header := strings.TrimSpace(q[:openParenIdx])
		headerParts := strings.Fields(header)
		if len(headerParts) == 0 {
			continue
		}
		tableName := headerParts[len(headerParts)-1]

		body := q[openParenIdx+1:]
		lastCloseParenIdx := strings.LastIndex(body, ")")
		if lastCloseParenIdx != -1 {
			body = body[:lastCloseParenIdx]
		}

		// Split by comma, attempting to avoid splitting on commas inside parentheses (e.g. DECIMAL(10,2))
		var columns []string
		var current strings.Builder
		parenCount := 0
		for _, r := range body {
			if r == '(' {
				parenCount++
			} else if r == ')' {
				parenCount--
			}
			if r == ',' && parenCount == 0 {
				columns = append(columns, current.String())
				current.Reset()
			} else {
				current.WriteRune(r)
			}
		}
		if current.Len() > 0 {
			columns = append(columns, current.String())
		}

		for _, colLine := range columns {
			colLine = strings.TrimSpace(colLine)
			if colLine == "" {
				continue
			}

			upperColLine := strings.ToUpper(colLine)
			if strings.HasPrefix(upperColLine, "PRIMARY KEY") ||
				strings.HasPrefix(upperColLine, "UNIQUE") ||
				strings.HasPrefix(upperColLine, "CONSTRAINT") ||
				strings.HasPrefix(upperColLine, "FOREIGN KEY") ||
				strings.HasPrefix(upperColLine, "CHECK") {
				continue
			}

			colParts := strings.Fields(colLine)
			if len(colParts) < 2 {
				continue
			}

			colName := colParts[0]
			colType := strings.Join(colParts[1:], " ")

			// Skip if it's ID or looks like a table-level constraint
			if strings.ToLower(colName) == "id" {
				continue
			}

			// Clean up colType for ALTER TABLE
			// SQLite doesn't like UNIQUE/PRIMARY KEY or REFERENCES in ADD COLUMN
			if s.driver == "sqlite" {
				colType = strings.ReplaceAll(colType, "UNIQUE", "")
				colType = strings.ReplaceAll(colType, "PRIMARY KEY", "")
				if idx := strings.Index(strings.ToUpper(colType), "REFERENCES"); idx != -1 {
					colType = colType[:idx]
				}
			}

			alterQuery := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", tableName, colName, colType)
			alterQuery = s.prepareQuery(alterQuery)
			_, err := s.db.ExecContext(ctx, alterQuery)
			if err != nil {
				errStr := strings.ToLower(err.Error())
				// Ignore if column already exists (SQLSTATE 42701 in Postgres, errno 1060 in MySQL)
				if strings.Contains(errStr, "already exists") ||
					strings.Contains(errStr, "duplicate") ||
					strings.Contains(errStr, "42701") ||
					strings.Contains(errStr, "1060") {
					continue
				}
				// Optionally log other migration errors
				// log.Printf("Auto-migration failed for %s.%s: %v", tableName, colName, err)
			}
		}
	}
}

func (s *sqlStorage) ListSources(ctx context.Context, filter storage.CommonFilter) ([]storage.Source, int, error) {
	baseQuery := s.queries.get(QueryListSources)
	countQuery := s.queries.get(QueryCountSources)
	var args []interface{}
	var where []string

	if filter.Search != "" {
		search := "%" + filter.Search + "%"
		where = append(where, "(id LIKE ? OR name LIKE ? OR type LIKE ? OR vhost LIKE ?)")
		args = append(args, search, search, search, search)
	}

	if filter.VHost != "" && filter.VHost != "all" {
		where = append(where, "vhost = ?")
		args = append(args, filter.VHost)
	}

	if filter.WorkspaceID != "" {
		where = append(where, "workspace_id = ?")
		args = append(args, filter.WorkspaceID)
	}

	if len(where) > 0 {
		baseQuery += " WHERE " + strings.Join(where, " AND ")
		countQuery += " WHERE " + strings.Join(where, " AND ")
	}

	var total int
	if err := s.queryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	if filter.Limit > 0 {
		baseQuery += " LIMIT ?"
		args = append(args, filter.Limit)
		if filter.Page > 0 {
			baseQuery += " OFFSET ?"
			args = append(args, (filter.Page-1)*filter.Limit)
		}
	}

	rows, err := s.query(ctx, baseQuery, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	sources := []storage.Source{}
	for rows.Next() {
		var src storage.Source
		var status, workerID, workspaceID, configStr, sample, stateStr sql.NullString
		if err := rows.Scan(&src.ID, &src.Name, &src.Type, &src.VHost, &src.Active, &status, &workerID, &workspaceID, &configStr, &sample, &stateStr); err != nil {
			return nil, 0, err
		}
		if status.Valid {
			src.Status = status.String
		}
		if workerID.Valid {
			src.WorkerID = workerID.String
		}
		if workspaceID.Valid {
			src.WorkspaceID = workspaceID.String
		}
		if sample.Valid {
			src.Sample = sample.String
		}
		if stateStr.Valid {
			if err := json.Unmarshal([]byte(stateStr.String), &src.State); err != nil {
				return nil, 0, err
			}
		}
		if configStr.Valid {
			if err := json.Unmarshal([]byte(configStr.String), &src.Config); err != nil {
				return nil, 0, err
			}
			src.Config = decryptConfig(src.Config)
		}
		sources = append(sources, src)
	}
	return sources, total, nil
}

func (s *sqlStorage) CreateSource(ctx context.Context, src storage.Source) error {
	configBytes, err := json.Marshal(encryptConfig(src.Config))
	if err != nil {
		return err
	}
	stateBytes, _ := json.Marshal(src.State)
	exec := func() error {
		_, e := s.exec(ctx, s.queries.get(QueryCreateSource),
			src.ID, src.Name, src.Type, src.VHost, src.Active, src.Status, src.WorkerID, src.WorkspaceID, string(configBytes), src.Sample, string(stateBytes))
		return e
	}
	return s.execWithRetry(ctx, exec)
}

func (s *sqlStorage) UpdateSource(ctx context.Context, src storage.Source) error {
	configBytes, err := json.Marshal(encryptConfig(src.Config))
	if err != nil {
		return err
	}
	stateBytes, _ := json.Marshal(src.State)
	exec := func() error {
		_, e := s.exec(ctx, s.queries.get(QueryUpdateSource),
			src.Name, src.Type, src.VHost, src.Active, src.Status, src.WorkerID, src.WorkspaceID, string(configBytes), src.Sample, string(stateBytes), src.ID)
		return e
	}
	return s.execWithRetry(ctx, exec)
}

func (s *sqlStorage) UpdateSourceStatus(ctx context.Context, id string, status string) error {
	exec := func() error {
		_, e := s.exec(ctx, s.queries.get(QueryUpdateSourceStatus), status, id)
		return e
	}
	return s.execWithRetry(ctx, exec)
}

func (s *sqlStorage) UpdateSourceState(ctx context.Context, id string, state map[string]string) error {
	stateBytes, err := json.Marshal(state)
	if err != nil {
		return err
	}
	exec := func() error {
		_, e := s.exec(ctx, s.queries.get(QueryUpdateSourceState), string(stateBytes), id)
		return e
	}
	return s.execWithRetry(ctx, exec)
}

func (s *sqlStorage) DeleteSource(ctx context.Context, id string) error {
	exec := func() error {
		_, e := s.exec(ctx, s.queries.get(QueryDeleteSource), id)
		return e
	}
	return s.execWithRetry(ctx, exec)
}

func (s *sqlStorage) GetSource(ctx context.Context, id string) (storage.Source, error) {
	var src storage.Source
	var status, workerID, workspaceID, configStr, sample, stateStr sql.NullString
	err := s.queryRow(ctx, s.queries.get(QueryGetSource), id).
		Scan(&src.ID, &src.Name, &src.Type, &src.VHost, &src.Active, &status, &workerID, &workspaceID, &configStr, &sample, &stateStr)
	if err == sql.ErrNoRows {
		return storage.Source{}, storage.ErrNotFound
	}
	if err != nil {
		return storage.Source{}, err
	}
	if status.Valid {
		src.Status = status.String
	}
	if workerID.Valid {
		src.WorkerID = workerID.String
	}
	if workspaceID.Valid {
		src.WorkspaceID = workspaceID.String
	}
	if sample.Valid {
		src.Sample = sample.String
	}
	if stateStr.Valid {
		if err := json.Unmarshal([]byte(stateStr.String), &src.State); err != nil {
			return storage.Source{}, err
		}
	}
	if configStr.Valid {
		if err := json.Unmarshal([]byte(configStr.String), &src.Config); err != nil {
			return storage.Source{}, err
		}
		src.Config = decryptConfig(src.Config)
	}
	return src, nil
}

func (s *sqlStorage) ListSinks(ctx context.Context, filter storage.CommonFilter) ([]storage.Sink, int, error) {
	baseQuery := s.queries.get(QueryListSinks)
	countQuery := s.queries.get(QueryCountSinks)
	var args []interface{}
	var where []string

	if filter.Search != "" {
		search := "%" + filter.Search + "%"
		where = append(where, "(id LIKE ? OR name LIKE ? OR type LIKE ? OR vhost LIKE ?)")
		args = append(args, search, search, search, search)
	}

	if filter.VHost != "" && filter.VHost != "all" {
		where = append(where, "vhost = ?")
		args = append(args, filter.VHost)
	}

	if filter.WorkspaceID != "" {
		where = append(where, "workspace_id = ?")
		args = append(args, filter.WorkspaceID)
	}

	if len(where) > 0 {
		baseQuery += " WHERE " + strings.Join(where, " AND ")
		countQuery += " WHERE " + strings.Join(where, " AND ")
	}

	var total int
	if err := s.queryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	if filter.Limit > 0 {
		baseQuery += " LIMIT ?"
		args = append(args, filter.Limit)
		if filter.Page > 0 {
			baseQuery += " OFFSET ?"
			args = append(args, (filter.Page-1)*filter.Limit)
		}
	}

	rows, err := s.query(ctx, baseQuery, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	sinks := []storage.Sink{}
	for rows.Next() {
		var snk storage.Sink
		var status, workerID, workspaceID, configStr sql.NullString
		if err := rows.Scan(&snk.ID, &snk.Name, &snk.Type, &snk.VHost, &snk.Active, &status, &workerID, &workspaceID, &configStr); err != nil {
			return nil, 0, err
		}
		if status.Valid {
			snk.Status = status.String
		}
		if workerID.Valid {
			snk.WorkerID = workerID.String
		}
		if workspaceID.Valid {
			snk.WorkspaceID = workspaceID.String
		}
		if configStr.Valid {
			if err := json.Unmarshal([]byte(configStr.String), &snk.Config); err != nil {
				return nil, 0, err
			}
			snk.Config = decryptConfig(snk.Config)
		}
		sinks = append(sinks, snk)
	}
	return sinks, total, nil
}

func (s *sqlStorage) CreateSink(ctx context.Context, snk storage.Sink) error {
	configBytes, err := json.Marshal(encryptConfig(snk.Config))
	if err != nil {
		return err
	}
	exec := func() error {
		_, e := s.exec(ctx, s.queries.get(QueryCreateSink),
			snk.ID, snk.Name, snk.Type, snk.VHost, snk.Active, snk.Status, snk.WorkerID, snk.WorkspaceID, string(configBytes))
		return e
	}
	return s.execWithRetry(ctx, exec)
}

func (s *sqlStorage) UpdateSink(ctx context.Context, snk storage.Sink) error {
	configBytes, err := json.Marshal(encryptConfig(snk.Config))
	if err != nil {
		return err
	}
	exec := func() error {
		_, e := s.exec(ctx, s.queries.get(QueryUpdateSink),
			snk.Name, snk.Type, snk.VHost, snk.Active, snk.Status, snk.WorkerID, snk.WorkspaceID, string(configBytes), snk.ID)
		return e
	}
	return s.execWithRetry(ctx, exec)
}

func (s *sqlStorage) UpdateSinkStatus(ctx context.Context, id string, status string) error {
	exec := func() error {
		_, e := s.exec(ctx, s.queries.get(QueryUpdateSinkStatus), status, id)
		return e
	}
	return s.execWithRetry(ctx, exec)
}

func (s *sqlStorage) DeleteSink(ctx context.Context, id string) error {
	exec := func() error {
		_, e := s.exec(ctx, s.queries.get(QueryDeleteSink), id)
		return e
	}
	return s.execWithRetry(ctx, exec)
}

func (s *sqlStorage) GetSink(ctx context.Context, id string) (storage.Sink, error) {
	var snk storage.Sink
	var status, workerID, workspaceID, configStr sql.NullString
	err := s.queryRow(ctx, s.queries.get(QueryGetSink), id).
		Scan(&snk.ID, &snk.Name, &snk.Type, &snk.VHost, &snk.Active, &status, &workerID, &workspaceID, &configStr)
	if err == sql.ErrNoRows {
		return storage.Sink{}, storage.ErrNotFound
	}
	if err != nil {
		return storage.Sink{}, err
	}
	if status.Valid {
		snk.Status = status.String
	}
	if workerID.Valid {
		snk.WorkerID = workerID.String
	}
	if workspaceID.Valid {
		snk.WorkspaceID = workspaceID.String
	}
	if configStr.Valid {
		if err := json.Unmarshal([]byte(configStr.String), &snk.Config); err != nil {
			return storage.Sink{}, err
		}
		snk.Config = decryptConfig(snk.Config)
	}
	return snk, nil
}

// isSQLiteBusyError returns true if the error appears to be a SQLite busy/locked condition.
func isSQLiteBusyError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	// modernc.org/sqlite typically formats as: "database is locked (5) (SQLITE_BUSY)"
	// We also check for generic "database is locked" and SQLITE_BUSY presence.
	return strings.Contains(msg, "database is locked") || strings.Contains(msg, "sqlite_busy")
}

// execWithRetry executes the provided function, retrying on SQLITE_BUSY with exponential backoff.
// Respects context cancellation/deadlines.
func (s *sqlStorage) execWithRetry(ctx context.Context, fn func() error) error {
	// Fast path
	if err := fn(); err != nil {
		if !isSQLiteBusyError(err) {
			return err
		}
		// Retry on busy below
		backoff := 50 * time.Millisecond
		// total ~ 50ms + 100 + 200 + 400 + 800 + 1600 ~= 3.2s
		const maxAttempts = 6
		var lastErr error
		for i := 1; i < maxAttempts; i++ {
			// Wait respecting context
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			if e := fn(); e == nil {
				return nil
			} else {
				lastErr = e
				if !isSQLiteBusyError(e) {
					return e
				}
			}
			// Exponential backoff with cap
			if backoff < 2*time.Second {
				backoff *= 2
				if backoff > 2*time.Second {
					backoff = 2 * time.Second
				}
			}
		}
		return lastErr
	}
	return nil
}

func (s *sqlStorage) ListUsers(ctx context.Context, filter storage.CommonFilter) ([]storage.User, int, error) {
	baseQuery := s.queries.get(QueryListUsers)
	countQuery := s.queries.get(QueryCountUsers)
	var args []interface{}
	var where []string

	if filter.Search != "" {
		search := "%" + filter.Search + "%"
		where = append(where, "(id LIKE ? OR username LIKE ? OR full_name LIKE ? OR email LIKE ? OR role LIKE ?)")
		args = append(args, search, search, search, search, search)
	}

	if len(where) > 0 {
		baseQuery += " WHERE " + strings.Join(where, " AND ")
		countQuery += " WHERE " + strings.Join(where, " AND ")
	}

	var total int
	if err := s.queryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	if filter.Limit > 0 {
		baseQuery += " LIMIT ?"
		args = append(args, filter.Limit)
		if filter.Page > 0 {
			baseQuery += " OFFSET ?"
			args = append(args, (filter.Page-1)*filter.Limit)
		}
	}

	rows, err := s.query(ctx, baseQuery, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	users := []storage.User{}
	for rows.Next() {
		var user storage.User
		var vhostsStr string
		if err := rows.Scan(&user.ID, &user.Username, &user.FullName, &user.Email, &user.Role, &vhostsStr, &user.TwoFactorEnabled); err != nil {
			return nil, 0, err
		}
		if vhostsStr != "" {
			if err := json.Unmarshal([]byte(vhostsStr), &user.VHosts); err != nil {
				return nil, 0, err
			}
		}
		users = append(users, user)
	}
	return users, total, nil
}

func (s *sqlStorage) CreateUser(ctx context.Context, user storage.User) error {
	vhostsBytes, err := json.Marshal(user.VHosts)
	if err != nil {
		return err
	}
	_, err = s.exec(ctx, s.queries.get(QueryCreateUser),
		user.ID, user.Username, user.Password, user.FullName, user.Email, user.Role, string(vhostsBytes), user.TwoFactorEnabled, user.TwoFactorSecret)
	return err
}

func (s *sqlStorage) UpdateUser(ctx context.Context, user storage.User) error {
	vhostsBytes, err := json.Marshal(user.VHosts)
	if err != nil {
		return err
	}
	if user.Password != "" {
		_, err = s.exec(ctx, s.queries.get(QueryUpdateUser),
			user.Username, user.Password, user.FullName, user.Email, user.Role, string(vhostsBytes), user.TwoFactorEnabled, user.TwoFactorSecret, user.ID)
	} else {
		_, err = s.exec(ctx, s.queries.get(QueryUpdateUserNoPassword),
			user.Username, user.FullName, user.Email, user.Role, string(vhostsBytes), user.TwoFactorEnabled, user.TwoFactorSecret, user.ID)
	}
	return err
}

func (s *sqlStorage) DeleteUser(ctx context.Context, id string) error {
	_, err := s.exec(ctx, s.queries.get(QueryDeleteUser), id)
	return err
}

func (s *sqlStorage) GetUser(ctx context.Context, id string) (storage.User, error) {
	var user storage.User
	var vhostsStr string
	err := s.queryRow(ctx, s.queries.get(QueryGetUser), id).
		Scan(&user.ID, &user.Username, &user.Password, &user.FullName, &user.Email, &user.Role, &vhostsStr, &user.TwoFactorEnabled, &user.TwoFactorSecret)
	if err == sql.ErrNoRows {
		return storage.User{}, storage.ErrNotFound
	}
	if err != nil {
		return storage.User{}, err
	}
	if vhostsStr != "" {
		if err := json.Unmarshal([]byte(vhostsStr), &user.VHosts); err != nil {
			return storage.User{}, err
		}
	}
	return user, nil
}

func (s *sqlStorage) GetUserByUsername(ctx context.Context, username string) (storage.User, error) {
	var user storage.User
	var vhostsStr string
	err := s.queryRow(ctx, s.queries.get(QueryGetUserByUsername), username).
		Scan(&user.ID, &user.Username, &user.Password, &user.FullName, &user.Email, &user.Role, &vhostsStr, &user.TwoFactorEnabled, &user.TwoFactorSecret)
	if err == sql.ErrNoRows {
		return storage.User{}, storage.ErrNotFound
	}
	if err != nil {
		return storage.User{}, err
	}
	if vhostsStr != "" {
		if err := json.Unmarshal([]byte(vhostsStr), &user.VHosts); err != nil {
			return storage.User{}, err
		}
	}
	return user, nil
}

func (s *sqlStorage) GetUserByEmail(ctx context.Context, email string) (storage.User, error) {
	var user storage.User
	var vhostsStr string
	err := s.queryRow(ctx, s.queries.get(QueryGetUserByEmail), email).
		Scan(&user.ID, &user.Username, &user.Password, &user.FullName, &user.Email, &user.Role, &vhostsStr, &user.TwoFactorEnabled, &user.TwoFactorSecret)
	if err == sql.ErrNoRows {
		return storage.User{}, storage.ErrNotFound
	}
	if err != nil {
		return storage.User{}, err
	}
	if vhostsStr != "" {
		if err := json.Unmarshal([]byte(vhostsStr), &user.VHosts); err != nil {
			return storage.User{}, err
		}
	}
	return user, nil
}

func (s *sqlStorage) ListWorkspaces(ctx context.Context) ([]storage.Workspace, error) {
	rows, err := s.query(ctx, s.queries.get(QueryListWorkspaces))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	wss := []storage.Workspace{}
	for rows.Next() {
		var ws storage.Workspace
		if err := rows.Scan(&ws.ID, &ws.Name, &ws.Description, &ws.MaxWorkflows, &ws.MaxCPU, &ws.MaxMemory, &ws.MaxThroughput, &ws.CreatedAt); err != nil {
			return nil, err
		}
		wss = append(wss, ws)
	}
	return wss, nil
}

func (s *sqlStorage) CreateWorkspace(ctx context.Context, ws storage.Workspace) error {
	if ws.ID == "" {
		ws.ID = uuid.New().String()
	}
	if ws.CreatedAt.IsZero() {
		ws.CreatedAt = time.Now()
	}
	exec := func() error {
		_, e := s.exec(ctx, s.queries.get(QueryCreateWorkspace), ws.ID, ws.Name, ws.Description, ws.MaxWorkflows, ws.MaxCPU, ws.MaxMemory, ws.MaxThroughput, ws.CreatedAt)
		return e
	}
	return s.execWithRetry(ctx, exec)
}

func (s *sqlStorage) DeleteWorkspace(ctx context.Context, id string) error {
	exec := func() error {
		_, e := s.exec(ctx, s.queries.get(QueryDeleteWorkspace), id)
		return e
	}
	return s.execWithRetry(ctx, exec)
}

func (s *sqlStorage) GetWorkspace(ctx context.Context, id string) (storage.Workspace, error) {
	row := s.db.QueryRowContext(ctx, s.queries.get(QueryGetWorkspace), id)
	var ws storage.Workspace
	err := row.Scan(&ws.ID, &ws.Name, &ws.Description, &ws.MaxWorkflows, &ws.MaxCPU, &ws.MaxMemory, &ws.MaxThroughput, &ws.CreatedAt)
	if err != nil {
		return storage.Workspace{}, err
	}
	return ws, nil
}

func (s *sqlStorage) ListVHosts(ctx context.Context, filter storage.CommonFilter) ([]storage.VHost, int, error) {
	baseQuery := s.queries.get(QueryListVHosts)
	countQuery := s.queries.get(QueryCountVHosts)
	var args []interface{}
	var where []string

	if filter.Search != "" {
		search := "%" + filter.Search + "%"
		where = append(where, "(id LIKE ? OR name LIKE ? OR description LIKE ?)")
		args = append(args, search, search, search)
	}

	if len(where) > 0 {
		baseQuery += " WHERE " + strings.Join(where, " AND ")
		countQuery += " WHERE " + strings.Join(where, " AND ")
	}

	var total int
	if err := s.queryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	if filter.Limit > 0 {
		baseQuery += " LIMIT ?"
		args = append(args, filter.Limit)
		if filter.Page > 0 {
			baseQuery += " OFFSET ?"
			args = append(args, (filter.Page-1)*filter.Limit)
		}
	}

	rows, err := s.query(ctx, baseQuery, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	vhosts := []storage.VHost{}
	for rows.Next() {
		var vhost storage.VHost
		if err := rows.Scan(&vhost.ID, &vhost.Name, &vhost.Description); err != nil {
			return nil, 0, err
		}
		vhosts = append(vhosts, vhost)
	}
	return vhosts, total, nil
}

func (s *sqlStorage) CreateVHost(ctx context.Context, vhost storage.VHost) error {
	_, err := s.exec(ctx, s.queries.get(QueryCreateVHost),
		vhost.ID, vhost.Name, vhost.Description)
	return err
}

func (s *sqlStorage) DeleteVHost(ctx context.Context, id string) error {
	_, err := s.exec(ctx, s.queries.get(QueryDeleteVHost), id)
	return err
}

func (s *sqlStorage) GetVHost(ctx context.Context, id string) (storage.VHost, error) {
	var vhost storage.VHost
	err := s.queryRow(ctx, s.queries.get(QueryGetVHost), id).
		Scan(&vhost.ID, &vhost.Name, &vhost.Description)
	if err == sql.ErrNoRows {
		return storage.VHost{}, storage.ErrNotFound
	}
	if err != nil {
		return storage.VHost{}, err
	}
	return vhost, nil
}

// ListWorkflows returns a page of workflows matching the provided filter along with the total count.
// It supports optional vhost scoping, fuzzy name search, and pagination via Limit/Page fields.
func (s *sqlStorage) ListWorkflows(ctx context.Context, filter storage.CommonFilter) ([]storage.Workflow, int, error) {
	query := s.queries.get(QueryListWorkflows)
	if !strings.Contains(strings.ToUpper(query), "WHERE") {
		query += " WHERE 1=1"
	}
	args := []interface{}{}

	if filter.VHost != "" && filter.VHost != "all" {
		query += " AND vhost = ?"
		args = append(args, filter.VHost)
	}

	if filter.Search != "" {
		query += " AND name LIKE ?"
		args = append(args, "%"+filter.Search+"%")
	}

	if filter.WorkspaceID != "" {
		query += " AND workspace_id = ?"
		args = append(args, filter.WorkspaceID)
	}

	var total int
	countQuery := s.queries.get(QueryCountWorkflows)
	if !strings.Contains(strings.ToUpper(countQuery), "WHERE") {
		countQuery += " WHERE 1=1"
	}
	countArgs := []interface{}{}
	if filter.VHost != "" && filter.VHost != "all" {
		countQuery += " AND vhost = ?"
		countArgs = append(countArgs, filter.VHost)
	}
	if filter.WorkspaceID != "" {
		countQuery += " AND workspace_id = ?"
		countArgs = append(countArgs, filter.WorkspaceID)
	}
	if filter.Search != "" {
		countQuery += " AND name LIKE ?"
		countArgs = append(countArgs, "%"+filter.Search+"%")
	}

	if err := s.queryRow(ctx, countQuery, countArgs...).Scan(&total); err != nil {
		return nil, 0, err
	}

	if filter.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, filter.Limit)
		if filter.Page > 0 {
			query += " OFFSET ?"
			args = append(args, (filter.Page-1)*filter.Limit)
		}
	}

	rows, err := s.query(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	wfs := []storage.Workflow{}
	for rows.Next() {
		var wf storage.Workflow
		var nodesJSON, edgesJSON, tagsJSON sql.NullString
		var leaseUntil sql.NullTime
		var ownerID sql.NullString
		var dlqSinkID, retryInterval, reconnectInterval sql.NullString
		var prioritizeDLQ, dryRun sql.NullBool
		var maxRetries, retentionDays, dlqThreshold sql.NullInt64
		var schemaType, schema, cron, idleTimeout, tier, workspaceID, traceRetention, auditRetention sql.NullString
		var traceSampleRate sql.NullFloat64
		var cpuReq, memReq sql.NullFloat64
		var throughputReq sql.NullInt64
		if err := rows.Scan(&wf.ID, &wf.Name, &wf.VHost, &wf.Active, &wf.Status, &wf.WorkerID, &ownerID, &leaseUntil, &nodesJSON, &edgesJSON, &dlqSinkID, &prioritizeDLQ, &maxRetries, &retryInterval, &reconnectInterval, &dryRun, &schemaType, &schema, &retentionDays, &cron, &idleTimeout, &tier, &traceSampleRate, &dlqThreshold, &tagsJSON, &workspaceID, &traceRetention, &auditRetention, &cpuReq, &memReq, &throughputReq); err != nil {
			return nil, 0, err
		}
		if cpuReq.Valid {
			wf.CPURequest = cpuReq.Float64
		}
		if memReq.Valid {
			wf.MemoryRequest = memReq.Float64
		}
		if throughputReq.Valid {
			wf.ThroughputRequest = int(throughputReq.Int64)
		}
		if traceRetention.Valid {
			wf.TraceRetention = traceRetention.String
		}
		if auditRetention.Valid {
			wf.AuditRetention = auditRetention.String
		}
		if workspaceID.Valid {
			wf.WorkspaceID = workspaceID.String
		}
		if dryRun.Valid {
			wf.DryRun = dryRun.Bool
		}
		if traceSampleRate.Valid {
			wf.TraceSampleRate = traceSampleRate.Float64
		}
		if ownerID.Valid {
			wf.OwnerID = ownerID.String
		}
		if leaseUntil.Valid {
			t := leaseUntil.Time
			wf.LeaseUntil = &t
		}
		if dlqSinkID.Valid {
			wf.DeadLetterSinkID = dlqSinkID.String
		}
		if prioritizeDLQ.Valid {
			wf.PrioritizeDLQ = prioritizeDLQ.Bool
		}
		if maxRetries.Valid {
			wf.MaxRetries = int(maxRetries.Int64)
		}
		if dlqThreshold.Valid {
			wf.DLQThreshold = int(dlqThreshold.Int64)
		}
		if retryInterval.Valid {
			wf.RetryInterval = retryInterval.String
		}
		if reconnectInterval.Valid {
			wf.ReconnectInterval = reconnectInterval.String
		}
		if schemaType.Valid {
			wf.SchemaType = schemaType.String
		}
		if schema.Valid {
			wf.Schema = schema.String
		}
		if cron.Valid {
			wf.Cron = cron.String
		}
		if idleTimeout.Valid {
			wf.IdleTimeout = idleTimeout.String
		}
		if tier.Valid {
			wf.Tier = storage.WorkflowTier(tier.String)
		}
		if retentionDays.Valid {
			val := int(retentionDays.Int64)
			wf.RetentionDays = &val
		}
		if nodesJSON.Valid && nodesJSON.String != "" {
			json.Unmarshal([]byte(nodesJSON.String), &wf.Nodes)
		}
		if edgesJSON.Valid && edgesJSON.String != "" {
			json.Unmarshal([]byte(edgesJSON.String), &wf.Edges)
		}
		if tagsJSON.Valid && tagsJSON.String != "" {
			json.Unmarshal([]byte(tagsJSON.String), &wf.Tags)
		}
		wfs = append(wfs, wf)
	}
	return wfs, total, nil
}

func (s *sqlStorage) CreateWorkflow(ctx context.Context, wf storage.Workflow) error {
	nodesJSON, _ := json.Marshal(wf.Nodes)
	edgesJSON, _ := json.Marshal(wf.Edges)
	tagsJSON, _ := json.Marshal(wf.Tags)
	if wf.ID == "" {
		wf.ID = uuid.New().String()
	}
	exec := func() error {
		_, e := s.exec(ctx,
			s.queries.get(QueryCreateWorkflow),
			wf.ID, wf.Name, wf.VHost, wf.Active, wf.Status, wf.WorkerID, string(nodesJSON), string(edgesJSON), wf.DeadLetterSinkID, wf.PrioritizeDLQ, wf.MaxRetries, wf.RetryInterval, wf.ReconnectInterval, wf.DryRun, wf.SchemaType, wf.Schema, wf.RetentionDays, wf.Cron, wf.IdleTimeout, string(wf.Tier), wf.TraceSampleRate, wf.DLQThreshold, string(tagsJSON), wf.WorkspaceID, wf.TraceRetention, wf.AuditRetention, wf.CPURequest, wf.MemoryRequest, wf.ThroughputRequest,
		)
		return e
	}
	return s.execWithRetry(ctx, exec)
}

// UpdateWorkflow updates an existing workflow's metadata and topology (nodes/edges) by ID.
func (s *sqlStorage) UpdateWorkflow(ctx context.Context, wf storage.Workflow) error {
	nodesJSON, _ := json.Marshal(wf.Nodes)
	edgesJSON, _ := json.Marshal(wf.Edges)
	tagsJSON, _ := json.Marshal(wf.Tags)
	exec := func() error {
		_, e := s.exec(ctx,
			s.queries.get(QueryUpdateWorkflow),
			wf.Name, wf.VHost, wf.Active, wf.Status, wf.WorkerID, string(nodesJSON), string(edgesJSON), wf.DeadLetterSinkID, wf.PrioritizeDLQ, wf.MaxRetries, wf.RetryInterval, wf.ReconnectInterval, wf.DryRun, wf.SchemaType, wf.Schema, wf.RetentionDays, wf.Cron, wf.IdleTimeout, string(wf.Tier), wf.TraceSampleRate, wf.DLQThreshold, string(tagsJSON), wf.WorkspaceID, wf.TraceRetention, wf.AuditRetention, wf.CPURequest, wf.MemoryRequest, wf.ThroughputRequest, wf.ID,
		)
		return e
	}
	return s.execWithRetry(ctx, exec)
}

func (s *sqlStorage) UpdateWorkflowStatus(ctx context.Context, id string, status string) error {
	exec := func() error {
		_, e := s.exec(ctx, s.queries.get(QueryUpdateWorkflowStatus), status, id)
		return e
	}
	return s.execWithRetry(ctx, exec)
}

func (s *sqlStorage) DeleteWorkflow(ctx context.Context, id string) error {
	exec := func() error {
		_, e := s.exec(ctx, s.queries.get(QueryDeleteWorkflow), id)
		return e
	}
	return s.execWithRetry(ctx, exec)
}

func (s *sqlStorage) GetWorkflow(ctx context.Context, id string) (storage.Workflow, error) {
	row := s.queryRow(ctx, s.queries.get(QueryGetWorkflow), id)
	var wf storage.Workflow
	var nodesJSON, edgesJSON, tagsJSON sql.NullString
	var leaseUntil sql.NullTime
	var ownerID sql.NullString
	var dlqSinkID, retryInterval, reconnectInterval sql.NullString
	var prioritizeDLQ, dryRun sql.NullBool
	var maxRetries, retentionDays, dlqThreshold sql.NullInt64
	var schemaType, schema, cron, idleTimeout, tier, workspaceID, traceRetention, auditRetention sql.NullString
	var traceSampleRate sql.NullFloat64
	var cpuReq, memReq sql.NullFloat64
	var throughputReq sql.NullInt64
	if err := row.Scan(&wf.ID, &wf.Name, &wf.VHost, &wf.Active, &wf.Status, &wf.WorkerID, &ownerID, &leaseUntil, &nodesJSON, &edgesJSON, &dlqSinkID, &prioritizeDLQ, &maxRetries, &retryInterval, &reconnectInterval, &dryRun, &schemaType, &schema, &retentionDays, &cron, &idleTimeout, &tier, &traceSampleRate, &dlqThreshold, &tagsJSON, &workspaceID, &traceRetention, &auditRetention, &cpuReq, &memReq, &throughputReq); err != nil {
		if err == sql.ErrNoRows {
			return storage.Workflow{}, storage.ErrNotFound
		}
		return storage.Workflow{}, err
	}
	if cpuReq.Valid {
		wf.CPURequest = cpuReq.Float64
	}
	if memReq.Valid {
		wf.MemoryRequest = memReq.Float64
	}
	if throughputReq.Valid {
		wf.ThroughputRequest = int(throughputReq.Int64)
	}
	if traceRetention.Valid {
		wf.TraceRetention = traceRetention.String
	}
	if auditRetention.Valid {
		wf.AuditRetention = auditRetention.String
	}
	if workspaceID.Valid {
		wf.WorkspaceID = workspaceID.String
	}
	if dryRun.Valid {
		wf.DryRun = dryRun.Bool
	}
	if traceSampleRate.Valid {
		wf.TraceSampleRate = traceSampleRate.Float64
	}
	if ownerID.Valid {
		wf.OwnerID = ownerID.String
	}
	if leaseUntil.Valid {
		t := leaseUntil.Time
		wf.LeaseUntil = &t
	}
	if dlqSinkID.Valid {
		wf.DeadLetterSinkID = dlqSinkID.String
	}
	if prioritizeDLQ.Valid {
		wf.PrioritizeDLQ = prioritizeDLQ.Bool
	}
	if maxRetries.Valid {
		wf.MaxRetries = int(maxRetries.Int64)
	}
	if dlqThreshold.Valid {
		wf.DLQThreshold = int(dlqThreshold.Int64)
	}
	if retryInterval.Valid {
		wf.RetryInterval = retryInterval.String
	}
	if reconnectInterval.Valid {
		wf.ReconnectInterval = reconnectInterval.String
	}
	if schemaType.Valid {
		wf.SchemaType = schemaType.String
	}
	if schema.Valid {
		wf.Schema = schema.String
	}
	if cron.Valid {
		wf.Cron = cron.String
	}
	if idleTimeout.Valid {
		wf.IdleTimeout = idleTimeout.String
	}
	if tier.Valid {
		wf.Tier = storage.WorkflowTier(tier.String)
	}
	if retentionDays.Valid {
		val := int(retentionDays.Int64)
		wf.RetentionDays = &val
	}
	if nodesJSON.Valid && nodesJSON.String != "" {
		json.Unmarshal([]byte(nodesJSON.String), &wf.Nodes)
	}
	if edgesJSON.Valid && edgesJSON.String != "" {
		json.Unmarshal([]byte(edgesJSON.String), &wf.Edges)
	}
	if tagsJSON.Valid && tagsJSON.String != "" {
		json.Unmarshal([]byte(tagsJSON.String), &wf.Tags)
	}
	return wf, nil
}

// AcquireWorkflowLease attempts to acquire or re-acquire a workflow lease.
// It succeeds if the workflow is unowned, expired, or already owned by this owner.
func (s *sqlStorage) AcquireWorkflowLease(ctx context.Context, workflowID, ownerID string, ttlSeconds int) (bool, error) {
	if ttlSeconds <= 0 {
		ttlSeconds = 30
	}
	now := time.Now().UTC()
	until := now.Add(time.Duration(ttlSeconds) * time.Second)
	var res sql.Result
	exec := func() error {
		var e error
		res, e = s.exec(ctx,
			s.queries.get(QueryAcquireLease),
			ownerID, until, workflowID, now, ownerID,
		)
		return e
	}
	if err := s.execWithRetry(ctx, exec); err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// RenewWorkflowLease extends an existing lease only if owned by ownerID and not yet expired.
func (s *sqlStorage) RenewWorkflowLease(ctx context.Context, workflowID, ownerID string, ttlSeconds int) (bool, error) {
	if ttlSeconds <= 0 {
		ttlSeconds = 30
	}
	now := time.Now().UTC()
	until := now.Add(time.Duration(ttlSeconds) * time.Second)
	var res sql.Result
	exec := func() error {
		var e error
		res, e = s.exec(ctx,
			s.queries.get(QueryRenewLease),
			until, workflowID, ownerID, now,
		)
		return e
	}
	if err := s.execWithRetry(ctx, exec); err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ReleaseWorkflowLease clears ownership if owned by ownerID.
func (s *sqlStorage) ReleaseWorkflowLease(ctx context.Context, workflowID, ownerID string) error {
	exec := func() error {
		_, e := s.exec(ctx,
			s.queries.get(QueryReleaseLease),
			workflowID, ownerID,
		)
		return e
	}
	return s.execWithRetry(ctx, exec)
}

func (s *sqlStorage) ListWorkers(ctx context.Context, filter storage.CommonFilter) ([]storage.Worker, int, error) {
	baseQuery := s.queries.get(QueryListWorkers)
	countQuery := s.queries.get(QueryCountWorkers)
	var args []interface{}
	var where []string

	if filter.Search != "" {
		search := "%" + filter.Search + "%"
		where = append(where, "(id LIKE ? OR name LIKE ? OR host LIKE ? OR description LIKE ?)")
		args = append(args, search, search, search, search)
	}

	if len(where) > 0 {
		baseQuery += " WHERE " + strings.Join(where, " AND ")
		countQuery += " WHERE " + strings.Join(where, " AND ")
	}

	var total int
	if err := s.queryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	if filter.Limit > 0 {
		baseQuery += " LIMIT ?"
		args = append(args, filter.Limit)
		if filter.Page > 0 {
			baseQuery += " OFFSET ?"
			args = append(args, (filter.Page-1)*filter.Limit)
		}
	}

	rows, err := s.query(ctx, baseQuery, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	workers := []storage.Worker{}
	for rows.Next() {
		var w storage.Worker
		var token sql.NullString
		var lastSeen sql.NullTime
		var cpu, mem sql.NullFloat64
		if err := rows.Scan(&w.ID, &w.Name, &w.Host, &w.Port, &w.Description, &token, &lastSeen, &cpu, &mem); err != nil {
			return nil, 0, err
		}
		if token.Valid {
			w.Token = token.String
		}
		if lastSeen.Valid {
			w.LastSeen = &lastSeen.Time
		}
		if cpu.Valid {
			w.CPUUsage = cpu.Float64
		}
		if mem.Valid {
			w.MemoryUsage = mem.Float64
		}
		workers = append(workers, w)
	}
	return workers, total, nil
}

func (s *sqlStorage) CreateWorker(ctx context.Context, worker storage.Worker) error {
	// Ensure ID and token are set to sane defaults when missing to simplify setup
	if worker.ID == "" {
		worker.ID = uuid.New().String()
	}
	if worker.Token == "" {
		worker.Token = uuid.New().String()
	}
	_, err := s.exec(ctx, s.queries.get(QueryCreateWorker),
		worker.ID, worker.Name, worker.Host, worker.Port, worker.Description, worker.Token, worker.LastSeen, worker.CPUUsage, worker.MemoryUsage)
	return err
}

func (s *sqlStorage) UpdateWorker(ctx context.Context, worker storage.Worker) error {
	_, err := s.exec(ctx, s.queries.get(QueryUpdateWorker),
		worker.Name, worker.Host, worker.Port, worker.Description, worker.Token, worker.LastSeen, worker.CPUUsage, worker.MemoryUsage, worker.ID)
	return err
}

func (s *sqlStorage) UpdateWorkerHeartbeat(ctx context.Context, id string, cpu, mem float64) error {
	_, err := s.exec(ctx, s.queries.get(QueryUpdateHeartbeat), time.Now(), cpu, mem, id)
	return err
}

func (s *sqlStorage) DeleteWorker(ctx context.Context, id string) error {
	_, err := s.exec(ctx, s.queries.get(QueryDeleteWorker), id)
	return err
}

func (s *sqlStorage) GetWorker(ctx context.Context, id string) (storage.Worker, error) {
	var w storage.Worker
	var token sql.NullString
	var lastSeen sql.NullTime
	var cpu, mem sql.NullFloat64
	err := s.queryRow(ctx, s.queries.get(QueryGetWorker), id).
		Scan(&w.ID, &w.Name, &w.Host, &w.Port, &w.Description, &token, &lastSeen, &cpu, &mem)
	if err == sql.ErrNoRows {
		return storage.Worker{}, storage.ErrNotFound
	}
	if err != nil {
		return w, err
	}
	if token.Valid {
		w.Token = token.String
	}
	if lastSeen.Valid {
		w.LastSeen = &lastSeen.Time
	}
	if cpu.Valid {
		w.CPUUsage = cpu.Float64
	}
	if mem.Valid {
		w.MemoryUsage = mem.Float64
	}
	return w, nil
}

func (s *sqlStorage) ListLogs(ctx context.Context, filter storage.LogFilter) ([]storage.Log, int, error) {
	where := " WHERE 1=1"
	var args []interface{}

	// Time bounds (if provided)
	if !filter.Since.IsZero() {
		where += " AND timestamp >= ?"
		args = append(args, filter.Since)
	}
	if !filter.Until.IsZero() {
		where += " AND timestamp < ?"
		args = append(args, filter.Until)
	}

	if filter.SourceID != "" {
		where += " AND source_id = ?"
		args = append(args, filter.SourceID)
	}
	if filter.SinkID != "" {
		where += " AND sink_id = ?"
		args = append(args, filter.SinkID)
	}
	if filter.WorkflowID != "" {
		where += " AND workflow_id = ?"
		args = append(args, filter.WorkflowID)
	}
	if filter.WithoutWorkflow {
		where += " AND workflow_id IS NULL"
	}
	if filter.Level != "" {
		where += " AND level = ?"
		args = append(args, filter.Level)
	}
	if filter.Action != "" {
		where += " AND action = ?"
		args = append(args, filter.Action)
	}
	if filter.Search != "" {
		search := "%" + filter.Search + "%"
		// Avoid scanning large 'data' payloads for LIKE to improve performance.
		// Search in message and identifiers only.
		where += " AND (message LIKE ? OR action LIKE ? OR source_id LIKE ? OR sink_id LIKE ? OR workflow_id LIKE ?)"
		args = append(args, search, search, search, search, search)
	}

	var total int
	countQuery := s.queries.get(QueryCountLogs) + where
	if err := s.queryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	querySuffix := where + " ORDER BY timestamp DESC"

	if filter.Limit > 0 {
		querySuffix += " LIMIT ?"
		args = append(args, filter.Limit)
		if filter.Page > 0 {
			querySuffix += " OFFSET ?"
			args = append(args, (filter.Page-1)*filter.Limit)
		}
	} else {
		querySuffix += " LIMIT 100"
	}

	query := s.queries.get(QueryListLogs) + querySuffix
	rows, err := s.query(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	logs := []storage.Log{}
	for rows.Next() {
		var l storage.Log
		var action, sourceID, sinkID, workflowID, userID, username, data sql.NullString
		if err := rows.Scan(&l.ID, &l.Timestamp, &l.Level, &l.Message, &action, &sourceID, &sinkID, &workflowID, &userID, &username, &data); err != nil {
			return nil, 0, err
		}
		if action.Valid {
			l.Action = action.String
		}
		if sourceID.Valid {
			l.SourceID = sourceID.String
		}
		if sinkID.Valid {
			l.SinkID = sinkID.String
		}
		if workflowID.Valid {
			l.WorkflowID = workflowID.String
		}
		if userID.Valid {
			l.UserID = userID.String
		}
		if username.Valid {
			l.Username = username.String
		}
		if data.Valid {
			l.Data = data.String
		}
		logs = append(logs, l)
	}
	return logs, total, nil
}

func (s *sqlStorage) CreateLog(ctx context.Context, l storage.Log) error {
	if l.ID == "" {
		l.ID = uuid.New().String()
	}
	if l.Timestamp.IsZero() {
		l.Timestamp = time.Now()
	}

	exec := func() error {
		_, e := s.exec(ctx, s.queries.get(QueryCreateLog),
			l.ID, l.Timestamp, l.Level, l.Message,
			sql.NullString{String: l.Action, Valid: l.Action != ""},
			sql.NullString{String: l.SourceID, Valid: l.SourceID != ""},
			sql.NullString{String: l.SinkID, Valid: l.SinkID != ""},
			sql.NullString{String: l.WorkflowID, Valid: l.WorkflowID != ""},
			sql.NullString{String: l.UserID, Valid: l.UserID != ""},
			sql.NullString{String: l.Username, Valid: l.Username != ""},
			sql.NullString{String: l.Data, Valid: l.Data != ""},
		)
		return e
	}
	return s.execWithRetry(ctx, exec)
}

func (s *sqlStorage) DeleteLogs(ctx context.Context, filter storage.LogFilter) error {
	query := s.queries.get(QueryDeleteLogs) + " WHERE 1=1"
	var args []interface{}

	if filter.SourceID != "" {
		query += " AND source_id = ?"
		args = append(args, filter.SourceID)
	}
	if filter.SinkID != "" {
		query += " AND sink_id = ?"
		args = append(args, filter.SinkID)
	}
	if filter.WorkflowID != "" {
		query += " AND workflow_id = ?"
		args = append(args, filter.WorkflowID)
	}
	if filter.WithoutWorkflow {
		query += " AND workflow_id IS NULL"
	}
	if filter.Level != "" {
		query += " AND level = ?"
		args = append(args, filter.Level)
	}
	if filter.Action != "" {
		query += " AND action = ?"
		args = append(args, filter.Action)
	}
	if !filter.Since.IsZero() {
		query += " AND timestamp >= ?"
		args = append(args, filter.Since)
	}
	if !filter.Until.IsZero() {
		query += " AND timestamp < ?"
		args = append(args, filter.Until)
	}

	exec := func() error {
		_, e := s.exec(ctx, query, args...)
		return e
	}
	return s.execWithRetry(ctx, exec)
}

func (s *sqlStorage) GetSetting(ctx context.Context, key string) (string, error) {
	var value string
	err := s.queryRow(ctx, s.queries.get(QueryGetSetting), key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}

func (s *sqlStorage) UpdateNodeState(ctx context.Context, workflowID, nodeID string, state interface{}) error {
	stateJSON, err := json.Marshal(state)
	if err != nil {
		return err
	}

	query := s.queries.get(QueryUpdateNodeState)

	_, err = s.db.ExecContext(ctx, query, workflowID, nodeID, string(stateJSON))
	return err
}

func (s *sqlStorage) GetNodeStates(ctx context.Context, workflowID string) (map[string]interface{}, error) {
	rows, err := s.query(ctx, "SELECT node_id, state FROM workflow_node_states WHERE workflow_id = ?", workflowID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	states := make(map[string]interface{})
	for rows.Next() {
		var nodeID, stateJSON string
		if err := rows.Scan(&nodeID, &stateJSON); err != nil {
			return nil, err
		}

		var state interface{}
		if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
			return nil, err
		}
		states[nodeID] = state
	}
	return states, nil
}

func (s *sqlStorage) SaveSetting(ctx context.Context, key string, value string) error {
	query := s.queries.get(QuerySaveSetting)
	exec := func() error {
		_, e := s.exec(ctx, query, key, value)
		return e
	}
	return s.execWithRetry(ctx, exec)
}

func (s *sqlStorage) CreateAuditLog(ctx context.Context, log storage.AuditLog) error {
	if log.ID == "" {
		log.ID = uuid.NewString()
	}
	if log.Timestamp.IsZero() {
		log.Timestamp = time.Now()
	}
	exec := func() error {
		_, err := s.exec(ctx, s.queries.get(QueryCreateAuditLog),
			log.ID, log.Timestamp, log.UserID, log.Username, log.Action, log.EntityType, log.EntityID, log.Payload, log.IP)
		return err
	}
	return s.execWithRetry(ctx, exec)
}

func (s *sqlStorage) PurgeAuditLogs(ctx context.Context, before time.Time) error {
	exec := func() error {
		_, err := s.exec(ctx, s.queries.get(QueryPurgeAuditLogs), before)
		return err
	}
	return s.execWithRetry(ctx, exec)
}

func (s *sqlStorage) PurgeMessageTraces(ctx context.Context, before time.Time) error {
	exec := func() error {
		_, err := s.exec(ctx, s.queries.get(QueryPurgeMessageTraces), before)
		return err
	}
	return s.execWithRetry(ctx, exec)
}

func (s *sqlStorage) CreateWebhookRequest(ctx context.Context, req storage.WebhookRequest) error {
	if req.ID == "" {
		req.ID = uuid.NewString()
	}
	if req.Timestamp.IsZero() {
		req.Timestamp = time.Now()
	}

	headersJSON, _ := json.Marshal(req.Headers)

	exec := func() error {
		_, err := s.exec(ctx, s.queries.get(QueryCreateWebhookRequest),
			req.ID, req.Timestamp, req.Path, req.Method, string(headersJSON), req.Body)
		if err != nil {
			return err
		}

		// Keep only last 50 requests per path to satisfy "last N" requirement
		_, err = s.exec(ctx, `DELETE FROM webhook_requests 
			WHERE path = ? AND id NOT IN (
				SELECT id FROM webhook_requests 
				WHERE path = ? 
				ORDER BY timestamp DESC 
				LIMIT 50
			)`, req.Path, req.Path)
		return err
	}
	return s.execWithRetry(ctx, exec)
}

func (s *sqlStorage) ListWebhookRequests(ctx context.Context, filter storage.WebhookRequestFilter) ([]storage.WebhookRequest, int, error) {
	var args []any
	where := "1=1"
	if filter.Path != "" {
		where += " AND path = ?"
		args = append(args, filter.Path)
	}

	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	page := filter.Page
	if page < 1 {
		page = 1
	}
	offset := (page - 1) * limit

	countQuery := "SELECT COUNT(*) FROM webhook_requests WHERE " + where
	var total int
	err := s.queryRow(ctx, countQuery, args...).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	query := "SELECT id, timestamp, path, method, headers, body FROM webhook_requests WHERE " + where + " ORDER BY timestamp DESC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)

	rows, err := s.query(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	requests := []storage.WebhookRequest{}
	for rows.Next() {
		var req storage.WebhookRequest
		var headersJSON string
		if err := rows.Scan(&req.ID, &req.Timestamp, &req.Path, &req.Method, &headersJSON, &req.Body); err != nil {
			return nil, 0, err
		}
		_ = json.Unmarshal([]byte(headersJSON), &req.Headers)
		requests = append(requests, req)
	}

	return requests, total, nil
}

func (s *sqlStorage) GetWebhookRequest(ctx context.Context, id string) (storage.WebhookRequest, error) {
	var req storage.WebhookRequest
	var headersJSON string
	err := s.queryRow(ctx, s.queries.get(QueryGetWebhookRequest), id).Scan(&req.ID, &req.Timestamp, &req.Path, &req.Method, &headersJSON, &req.Body)
	if err != nil {
		return storage.WebhookRequest{}, err
	}
	_ = json.Unmarshal([]byte(headersJSON), &req.Headers)
	return req, nil
}

func (s *sqlStorage) DeleteWebhookRequests(ctx context.Context, filter storage.WebhookRequestFilter) error {
	var args []any
	where := "1=1"
	if filter.Path != "" {
		where += " AND path = ?"
		args = append(args, filter.Path)
	}

	exec := func() error {
		_, err := s.exec(ctx, "DELETE FROM webhook_requests WHERE "+where, args...)
		return err
	}
	return s.execWithRetry(ctx, exec)
}

func (s *sqlStorage) CreateFormSubmission(ctx context.Context, sub storage.FormSubmission) error {
	exec := func() error {
		_, err := s.exec(ctx, s.queries.get(QueryCreateFormSubmission),
			sub.ID, sub.Timestamp, sub.Path, sub.Data, sub.Status)
		return err
	}
	return s.execWithRetry(ctx, exec)
}

func (s *sqlStorage) ListFormSubmissions(ctx context.Context, filter storage.FormSubmissionFilter) ([]storage.FormSubmission, int, error) {
	baseQuery := s.queries.get(QueryListFormSubmissions)
	countQuery := s.queries.get(QueryCountFormSubmissions)
	var args []any
	where := "1=1"

	if filter.Path != "" {
		where += " AND path = ?"
		args = append(args, filter.Path)
	}
	if filter.Status != "" {
		where += " AND status = ?"
		args = append(args, filter.Status)
	}

	baseQuery += " WHERE " + where
	countQuery += " WHERE " + where

	var total int
	err := s.queryRow(ctx, countQuery, args...).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	baseQuery += " ORDER BY timestamp ASC"
	if filter.Limit > 0 {
		offset := (filter.Page - 1) * filter.Limit
		if offset < 0 {
			offset = 0
		}
		baseQuery += fmt.Sprintf(" LIMIT %d OFFSET %d", filter.Limit, offset)
	}

	rows, err := s.query(ctx, baseQuery, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var submissions []storage.FormSubmission
	for rows.Next() {
		var sub storage.FormSubmission
		if err := rows.Scan(&sub.ID, &sub.Timestamp, &sub.Path, &sub.Data, &sub.Status); err != nil {
			return nil, 0, err
		}
		submissions = append(submissions, sub)
	}
	return submissions, total, nil
}

func (s *sqlStorage) GetFormSubmission(ctx context.Context, id string) (storage.FormSubmission, error) {
	var sub storage.FormSubmission
	err := s.queryRow(ctx, s.queries.get(QueryGetFormSubmission), id).Scan(&sub.ID, &sub.Timestamp, &sub.Path, &sub.Data, &sub.Status)
	return sub, err
}

func (s *sqlStorage) UpdateFormSubmissionStatus(ctx context.Context, id string, status string) error {
	exec := func() error {
		_, err := s.exec(ctx, s.queries.get(QueryUpdateFormSubmissionStatus), status, id)
		return err
	}
	return s.execWithRetry(ctx, exec)
}

func (s *sqlStorage) DeleteFormSubmissions(ctx context.Context, filter storage.FormSubmissionFilter) error {
	var args []any
	where := "1=1"
	if filter.Path != "" {
		where += " AND path = ?"
		args = append(args, filter.Path)
	}
	if filter.Status != "" {
		where += " AND status = ?"
		args = append(args, filter.Status)
	}

	exec := func() error {
		_, err := s.exec(ctx, "DELETE FROM form_submissions WHERE "+where, args...)
		return err
	}
	return s.execWithRetry(ctx, exec)
}

func (s *sqlStorage) ListAuditLogs(ctx context.Context, filter storage.AuditFilter) ([]storage.AuditLog, int, error) {
	baseQuery := "SELECT id, timestamp, user_id, username, action, entity_type, entity_id, payload, ip FROM audit_logs"
	countQuery := "SELECT COUNT(*) FROM audit_logs"
	var args []interface{}
	var where []string

	if filter.Search != "" {
		search := "%" + filter.Search + "%"
		where = append(where, "(id LIKE ? OR username LIKE ? OR action LIKE ? OR entity_id LIKE ? OR payload LIKE ?)")
		args = append(args, search, search, search, search, search)
	}
	if filter.UserID != "" {
		where = append(where, "user_id = ?")
		args = append(args, filter.UserID)
	}
	if filter.EntityType != "" {
		where = append(where, "entity_type = ?")
		args = append(args, filter.EntityType)
	}
	if filter.EntityID != "" {
		where = append(where, "entity_id = ?")
		args = append(args, filter.EntityID)
	}
	if filter.Action != "" {
		where = append(where, "action = ?")
		args = append(args, filter.Action)
	}
	if filter.From != nil {
		where = append(where, "timestamp >= ?")
		args = append(args, *filter.From)
	}
	if filter.To != nil {
		where = append(where, "timestamp <= ?")
		args = append(args, *filter.To)
	}

	if len(where) > 0 {
		baseQuery += " WHERE " + strings.Join(where, " AND ")
		countQuery += " WHERE " + strings.Join(where, " AND ")
	}

	var total int
	if err := s.queryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	baseQuery += " ORDER BY timestamp DESC"

	if filter.Limit > 0 {
		baseQuery += " LIMIT ?"
		args = append(args, filter.Limit)
		if filter.Page > 0 {
			baseQuery += " OFFSET ?"
			args = append(args, (filter.Page-1)*filter.Limit)
		}
	}

	rows, err := s.query(ctx, baseQuery, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	logs := []storage.AuditLog{}
	for rows.Next() {
		var log storage.AuditLog
		var payload, ip sql.NullString
		if err := rows.Scan(&log.ID, &log.Timestamp, &log.UserID, &log.Username, &log.Action, &log.EntityType, &log.EntityID, &payload, &ip); err != nil {
			return nil, 0, err
		}
		if payload.Valid {
			log.Payload = payload.String
		}
		if ip.Valid {
			log.IP = ip.String
		}
		logs = append(logs, log)
	}
	return logs, total, nil
}

func (s *sqlStorage) ListSchemas(ctx context.Context, name string) ([]storage.Schema, error) {
	rows, err := s.query(ctx, s.queries.get(QueryListSchemas), name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	schemas := []storage.Schema{}
	for rows.Next() {
		var sc storage.Schema
		if err := rows.Scan(&sc.ID, &sc.Name, &sc.Version, &sc.Type, &sc.Content, &sc.CreatedAt); err != nil {
			return nil, err
		}
		schemas = append(schemas, sc)
	}
	return schemas, nil
}

func (s *sqlStorage) ListAllSchemas(ctx context.Context) ([]storage.Schema, error) {
	rows, err := s.query(ctx, s.queries.get(QueryListAllSchemas))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	schemas := []storage.Schema{}
	for rows.Next() {
		var sc storage.Schema
		if err := rows.Scan(&sc.ID, &sc.Name, &sc.Version, &sc.Type, &sc.Content, &sc.CreatedAt); err != nil {
			return nil, err
		}
		schemas = append(schemas, sc)
	}
	return schemas, nil
}

func (s *sqlStorage) GetSchema(ctx context.Context, name string, version int) (storage.Schema, error) {
	var sc storage.Schema
	err := s.queryRow(ctx, s.queries.get(QueryGetSchema), name, version).Scan(&sc.ID, &sc.Name, &sc.Version, &sc.Type, &sc.Content, &sc.CreatedAt)
	if err == sql.ErrNoRows {
		return storage.Schema{}, fmt.Errorf("schema %s version %d not found", name, version)
	}
	return sc, err
}

func (s *sqlStorage) GetLatestSchema(ctx context.Context, name string) (storage.Schema, error) {
	var sc storage.Schema
	err := s.queryRow(ctx, s.queries.get(QueryGetLatestSchema), name).Scan(&sc.ID, &sc.Name, &sc.Version, &sc.Type, &sc.Content, &sc.CreatedAt)
	if err == sql.ErrNoRows {
		return storage.Schema{}, fmt.Errorf("schema %s not found", name)
	}
	return sc, err
}

func (s *sqlStorage) CreateSchema(ctx context.Context, sc storage.Schema) error {
	if sc.ID == "" {
		sc.ID = uuid.New().String()
	}
	if sc.CreatedAt.IsZero() {
		sc.CreatedAt = time.Now()
	}

	exec := func() error {
		_, err := s.exec(ctx, s.queries.get(QueryCreateSchema),
			sc.ID, sc.Name, sc.Version, sc.Type, sc.Content, sc.CreatedAt)
		return err
	}
	return s.execWithRetry(ctx, exec)
}

func (s *sqlStorage) RecordTraceStep(ctx context.Context, workflowID, messageID string, step hermod.TraceStep) error {
	id := uuid.New().String()
	dataBytes, _ := json.Marshal(step.Data)

	exec := func() error {
		_, err := s.exec(ctx, s.queries.get(QueryRecordTraceStep),
			id, messageID, workflowID, step.NodeID, step.Timestamp, step.Duration.Milliseconds(), string(dataBytes), step.Error)
		return err
	}
	return s.execWithRetry(ctx, exec)
}

func (s *sqlStorage) GetMessageTrace(ctx context.Context, workflowID, messageID string) (storage.MessageTrace, error) {
	rows, err := s.query(ctx, s.queries.get(QueryGetMessageTrace), workflowID, messageID)
	if err != nil {
		return storage.MessageTrace{}, err
	}
	defer rows.Close()

	var tr storage.MessageTrace
	tr.WorkflowID = workflowID
	tr.MessageID = messageID

	for rows.Next() {
		var step hermod.TraceStep
		var dataStr string
		var durationMs int64
		if err := rows.Scan(&step.NodeID, &step.Timestamp, &durationMs, &dataStr, &step.Error); err != nil {
			return storage.MessageTrace{}, err
		}
		step.Duration = time.Duration(durationMs) * time.Millisecond
		if dataStr != "" {
			_ = json.Unmarshal([]byte(dataStr), &step.Data)
		}
		tr.Steps = append(tr.Steps, step)
	}

	if len(tr.Steps) == 0 {
		return storage.MessageTrace{}, storage.ErrNotFound
	}

	tr.CreatedAt = tr.Steps[0].Timestamp
	return tr, nil
}

func (s *sqlStorage) ListMessageTraces(ctx context.Context, workflowID string, limit int) ([]storage.MessageTrace, error) {
	// This is a bit tricky with individual steps. We want unique message_ids.
	rows, err := s.query(ctx, s.queries.get(QueryListMessageTraces), workflowID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	traces := []storage.MessageTrace{}
	for rows.Next() {
		var tr storage.MessageTrace
		tr.WorkflowID = workflowID
		if err := rows.Scan(&tr.MessageID, &tr.CreatedAt); err != nil {
			return nil, err
		}
		traces = append(traces, tr)
	}
	return traces, nil
}

func (s *sqlStorage) CreateWorkflowVersion(ctx context.Context, v storage.WorkflowVersion) error {
	nodesJSON, err := json.Marshal(v.Nodes)
	if err != nil {
		return err
	}
	edgesJSON, err := json.Marshal(v.Edges)
	if err != nil {
		return err
	}

	_, err = s.exec(ctx, s.queries.get(QueryCreateWorkflowVersion),
		v.ID, v.WorkflowID, v.Version, string(nodesJSON), string(edgesJSON), v.Config, v.CreatedAt, v.CreatedBy, v.Message)
	return err
}

func (s *sqlStorage) ListWorkflowVersions(ctx context.Context, workflowID string) ([]storage.WorkflowVersion, error) {
	rows, err := s.query(ctx, s.queries.get(QueryListWorkflowVersions), workflowID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	versions := []storage.WorkflowVersion{}
	for rows.Next() {
		var v storage.WorkflowVersion
		var nodesStr, edgesStr string
		if err := rows.Scan(&v.ID, &v.WorkflowID, &v.Version, &nodesStr, &edgesStr, &v.Config, &v.CreatedAt, &v.CreatedBy, &v.Message); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(nodesStr), &v.Nodes); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(edgesStr), &v.Edges); err != nil {
			return nil, err
		}
		versions = append(versions, v)
	}
	return versions, nil
}

func (s *sqlStorage) GetWorkflowVersion(ctx context.Context, workflowID string, version int) (storage.WorkflowVersion, error) {
	var v storage.WorkflowVersion
	var nodesStr, edgesStr string
	err := s.queryRow(ctx, s.queries.get(QueryGetWorkflowVersion), workflowID, version).Scan(
		&v.ID, &v.WorkflowID, &v.Version, &nodesStr, &edgesStr, &v.Config, &v.CreatedAt, &v.CreatedBy, &v.Message)
	if err != nil {
		if err == sql.ErrNoRows {
			return v, storage.ErrNotFound
		}
		return v, err
	}
	if err := json.Unmarshal([]byte(nodesStr), &v.Nodes); err != nil {
		return v, err
	}
	if err := json.Unmarshal([]byte(edgesStr), &v.Edges); err != nil {
		return v, err
	}
	return v, nil
}

func (s *sqlStorage) CreateOutboxItem(ctx context.Context, item storage.OutboxItem) error {
	metaJSON, _ := json.Marshal(item.Metadata)
	exec := func() error {
		_, e := s.exec(ctx, s.queries.get(QueryCreateOutboxItem),
			item.ID, item.WorkflowID, item.SinkID, item.Payload, string(metaJSON), item.CreatedAt, item.Attempts, item.LastError, item.Status)
		return e
	}
	return s.execWithRetry(ctx, exec)
}

func (s *sqlStorage) ListOutboxItems(ctx context.Context, status string, limit int) ([]storage.OutboxItem, error) {
	rows, err := s.query(ctx, s.queries.get(QueryListOutboxItems), status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []storage.OutboxItem{}
	for rows.Next() {
		var item storage.OutboxItem
		var lastError, metaStr sql.NullString
		if err := rows.Scan(&item.ID, &item.WorkflowID, &item.SinkID, &item.Payload, &metaStr, &item.CreatedAt, &item.Attempts, &lastError, &item.Status); err != nil {
			return nil, err
		}
		if lastError.Valid {
			item.LastError = lastError.String
		}
		if metaStr.Valid && metaStr.String != "" {
			_ = json.Unmarshal([]byte(metaStr.String), &item.Metadata)
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *sqlStorage) DeleteOutboxItem(ctx context.Context, id string) error {
	exec := func() error {
		_, e := s.exec(ctx, s.queries.get(QueryDeleteOutboxItem), id)
		return e
	}
	return s.execWithRetry(ctx, exec)
}

func (s *sqlStorage) UpdateOutboxItem(ctx context.Context, item storage.OutboxItem) error {
	exec := func() error {
		_, e := s.exec(ctx, s.queries.get(QueryUpdateOutboxItem), item.Attempts, item.LastError, item.Status, item.ID)
		return e
	}
	return s.execWithRetry(ctx, exec)
}

func (s *sqlStorage) GetLineage(ctx context.Context) ([]storage.LineageEdge, error) {
	workflows, _, err := s.ListWorkflows(ctx, storage.CommonFilter{Limit: 1000})
	if err != nil {
		return nil, err
	}

	sources, _, err := s.ListSources(ctx, storage.CommonFilter{Limit: 1000})
	if err != nil {
		return nil, err
	}
	srcMap := make(map[string]storage.Source)
	for _, src := range sources {
		srcMap[src.ID] = src
	}

	sinks, _, err := s.ListSinks(ctx, storage.CommonFilter{Limit: 1000})
	if err != nil {
		return nil, err
	}
	snkMap := make(map[string]storage.Sink)
	for _, snk := range sinks {
		snkMap[snk.ID] = snk
	}

	lineage := []storage.LineageEdge{}
	for _, wf := range workflows {
		wfSources := []storage.Source{}
		wfSinks := []storage.Sink{}

		for _, node := range wf.Nodes {
			if node.Type == "source" {
				if src, ok := srcMap[node.RefID]; ok {
					wfSources = append(wfSources, src)
				}
			} else if node.Type == "sink" {
				if snk, ok := snkMap[node.RefID]; ok {
					wfSinks = append(wfSinks, snk)
				}
			}
		}

		for _, src := range wfSources {
			for _, snk := range wfSinks {
				lineage = append(lineage, storage.LineageEdge{
					SourceID:     src.ID,
					SourceName:   src.Name,
					SourceType:   src.Type,
					SinkID:       snk.ID,
					SinkName:     snk.Name,
					SinkType:     snk.Type,
					WorkflowID:   wf.ID,
					WorkflowName: wf.Name,
				})
			}
		}
	}

	return lineage, nil
}

func (s *sqlStorage) ListPlugins(ctx context.Context) ([]storage.Plugin, error) {
	rows, err := s.query(ctx, s.queries.get(QueryListPlugins))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var plugins []storage.Plugin
	for rows.Next() {
		var p storage.Plugin
		var installedAt sql.NullTime
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.Author, &p.Stars, &p.Category, &p.Certified, &p.Type, &p.WasmURL, &p.Installed, &installedAt); err != nil {
			return nil, err
		}
		if installedAt.Valid {
			p.InstalledAt = &installedAt.Time
		}
		plugins = append(plugins, p)
	}
	return plugins, nil
}

func (s *sqlStorage) GetPlugin(ctx context.Context, id string) (storage.Plugin, error) {
	var p storage.Plugin
	var installedAt sql.NullTime
	err := s.queryRow(ctx, s.queries.get(QueryGetPlugin), id).Scan(&p.ID, &p.Name, &p.Description, &p.Author, &p.Stars, &p.Category, &p.Certified, &p.Type, &p.WasmURL, &p.Installed, &installedAt)
	if err != nil {
		return p, err
	}
	if installedAt.Valid {
		p.InstalledAt = &installedAt.Time
	}
	return p, nil
}

func (s *sqlStorage) InstallPlugin(ctx context.Context, id string) error {
	exec := func() error {
		res, e := s.exec(ctx, s.queries.get(QueryInstallPlugin), time.Now(), id)
		if e != nil {
			return e
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return storage.ErrNotFound
		}
		return nil
	}
	return s.execWithRetry(ctx, exec)
}

func (s *sqlStorage) UninstallPlugin(ctx context.Context, id string) error {
	exec := func() error {
		res, e := s.exec(ctx, s.queries.get(QueryUninstallPlugin), id)
		if e != nil {
			return e
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return storage.ErrNotFound
		}
		return nil
	}
	return s.execWithRetry(ctx, exec)
}

func (s *sqlStorage) ListApprovals(ctx context.Context, filter storage.ApprovalFilter) ([]storage.Approval, int, error) {
	query := s.queries.get(QueryListApprovals)
	countQuery := s.queries.get(QueryCountApprovals)

	var where []string
	var args []any

	if filter.WorkflowID != "" {
		where = append(where, "workflow_id = ?")
		args = append(args, filter.WorkflowID)
	}
	if filter.Status != "" {
		where = append(where, "status = ?")
		args = append(args, filter.Status)
	}

	if len(where) > 0 {
		w := " WHERE " + strings.Join(where, " AND ")
		query += w
		countQuery += w
	}

	query += " ORDER BY created_at DESC"
	if filter.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d OFFSET %d", filter.Limit, filter.Page*filter.Limit)
	}

	var total int
	err := s.queryRow(ctx, countQuery, args...).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	rows, err := s.query(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var approvals []storage.Approval
	for rows.Next() {
		var a storage.Approval
		var metadata, data string
		var processedAt sql.NullTime
		var processedBy, notes sql.NullString
		err := rows.Scan(&a.ID, &a.WorkflowID, &a.NodeID, &a.MessageID, &a.Payload, &metadata, &data, &a.Status, &a.CreatedAt, &processedAt, &processedBy, &notes)
		if err != nil {
			return nil, 0, err
		}
		_ = json.Unmarshal([]byte(metadata), &a.Metadata)
		_ = json.Unmarshal([]byte(data), &a.Data)
		if processedAt.Valid {
			a.ProcessedAt = &processedAt.Time
		}
		a.ProcessedBy = processedBy.String
		a.Notes = notes.String
		approvals = append(approvals, a)
	}
	return approvals, total, nil
}

func (s *sqlStorage) CreateApproval(ctx context.Context, app storage.Approval) error {
	metadata, _ := json.Marshal(app.Metadata)
	data, _ := json.Marshal(app.Data)

	exec := func() error {
		_, err := s.exec(ctx, s.queries.get(QueryCreateApproval),
			app.ID, app.WorkflowID, app.NodeID, app.MessageID, app.Payload, string(metadata), string(data), app.Status, app.CreatedAt)
		return err
	}
	return s.execWithRetry(ctx, exec)
}

func (s *sqlStorage) GetApproval(ctx context.Context, id string) (storage.Approval, error) {
	var a storage.Approval
	var metadata, data string
	var processedAt sql.NullTime
	var processedBy, notes sql.NullString
	err := s.queryRow(ctx, s.queries.get(QueryGetApproval), id).Scan(
		&a.ID, &a.WorkflowID, &a.NodeID, &a.MessageID, &a.Payload, &metadata, &data, &a.Status, &a.CreatedAt, &processedAt, &processedBy, &notes)
	if err != nil {
		if err == sql.ErrNoRows {
			return a, storage.ErrNotFound
		}
		return a, err
	}
	_ = json.Unmarshal([]byte(metadata), &a.Metadata)
	_ = json.Unmarshal([]byte(data), &a.Data)
	if processedAt.Valid {
		a.ProcessedAt = &processedAt.Time
	}
	a.ProcessedBy = processedBy.String
	a.Notes = notes.String
	return a, nil
}

func (s *sqlStorage) UpdateApprovalStatus(ctx context.Context, id string, status string, processedBy string, notes string) error {
	exec := func() error {
		res, err := s.exec(ctx, s.queries.get(QueryUpdateApprovalStatus), status, time.Now(), processedBy, notes, id)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return storage.ErrNotFound
		}
		return nil
	}
	return s.execWithRetry(ctx, exec)
}

func (s *sqlStorage) DeleteApproval(ctx context.Context, id string) error {
	exec := func() error {
		res, err := s.exec(ctx, s.queries.get(QueryDeleteApproval), id)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return storage.ErrNotFound
		}
		return nil
	}
	return s.execWithRetry(ctx, exec)
}
