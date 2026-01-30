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
	db     *sql.DB
	driver string
}

func NewSQLStorage(db *sql.DB, driver string) storage.Storage {
	return &sqlStorage{db: db, driver: driver}
}

// preparePlaceholders rewrites parameter placeholders to match the current driver.
//
// Default (sqlite, mysql, mariadb) uses '?' and is passed through unchanged.
// Postgres (pgx) requires $1, $2, ...
// SQL Server (sqlserver) commonly uses @p1, @p2, ...
// This helper keeps SQL definitions central and simple while remaining portable.
func (s *sqlStorage) preparePlaceholders(query string) string {
	switch s.driver {
	case "pgx":
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

// exec wraps db.ExecContext with driver-specific placeholder preparation.
func (s *sqlStorage) exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	q := s.preparePlaceholders(query)
	return s.db.ExecContext(ctx, q, args...)
}

// query wraps db.QueryContext with driver-specific placeholder preparation.
func (s *sqlStorage) query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	q := s.preparePlaceholders(query)
	return s.db.QueryContext(ctx, q, args...)
}

// queryRow wraps db.QueryRowContext with driver-specific placeholder preparation.
func (s *sqlStorage) queryRow(ctx context.Context, query string, args ...any) *sql.Row {
	q := s.preparePlaceholders(query)
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

	queries := []string{
		`CREATE TABLE IF NOT EXISTS sources (
            id TEXT PRIMARY KEY,
            name TEXT NOT NULL UNIQUE,
            type TEXT,
            vhost TEXT,
            active BOOLEAN DEFAULT FALSE,
            status TEXT,
            worker_id TEXT,
            config TEXT,
            state TEXT
        )`,
		`CREATE TABLE IF NOT EXISTS sinks (
            id TEXT PRIMARY KEY,
            name TEXT NOT NULL UNIQUE,
            type TEXT,
            vhost TEXT,
            active BOOLEAN DEFAULT FALSE,
            status TEXT,
            worker_id TEXT,
            config TEXT
        )`,
		`CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			username TEXT UNIQUE,
			password TEXT,
			full_name TEXT,
			email TEXT,
			role TEXT,
			vhosts TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS vhosts (
			id TEXT PRIMARY KEY,
			name TEXT UNIQUE,
			description TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS workers (
			id TEXT PRIMARY KEY,
			name TEXT,
			host TEXT,
			port INTEGER,
			description TEXT,
			token TEXT,
			last_seen TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS logs (
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
		`CREATE TABLE IF NOT EXISTS workflows (
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
            tier TEXT
        )`,
		`CREATE TABLE IF NOT EXISTS workflow_node_states (
			workflow_id TEXT,
			node_id TEXT,
			state TEXT,
			PRIMARY KEY (workflow_id, node_id)
		)`,
		`CREATE TABLE IF NOT EXISTS webhook_requests (
			id TEXT PRIMARY KEY,
			timestamp TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			path TEXT,
			method TEXT,
			headers TEXT,
			body BLOB
		)`,
	}

	for _, q := range queries {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("failed to execute init query: %w", err)
		}
	}

	// Migrations: Add new columns if missing
	migrationQueries := []string{
		"ALTER TABLE sources ADD COLUMN worker_id TEXT",
		"ALTER TABLE sinks ADD COLUMN worker_id TEXT",
		"ALTER TABLE workers ADD COLUMN token TEXT",
		"ALTER TABLE workers ADD COLUMN last_seen TIMESTAMP",
		"ALTER TABLE sources ADD COLUMN active BOOLEAN DEFAULT FALSE",
		"ALTER TABLE sinks ADD COLUMN active BOOLEAN DEFAULT FALSE",
		"ALTER TABLE logs ADD COLUMN action TEXT",
		"ALTER TABLE logs ADD COLUMN workflow_id TEXT",
		"ALTER TABLE sources ADD COLUMN status TEXT",
		"ALTER TABLE sinks ADD COLUMN status TEXT",
		"ALTER TABLE sources ADD COLUMN sample TEXT",
		"ALTER TABLE sources ADD COLUMN state TEXT",
		"ALTER TABLE workflows ADD COLUMN owner_id TEXT",
		"ALTER TABLE workflows ADD COLUMN lease_until TIMESTAMP",
		"ALTER TABLE workflows ADD COLUMN dead_letter_sink_id TEXT",
		"ALTER TABLE workflows ADD COLUMN prioritize_dlq BOOLEAN DEFAULT FALSE",
		"ALTER TABLE workflows ADD COLUMN max_retries INTEGER DEFAULT 0",
		"ALTER TABLE workflows ADD COLUMN retry_interval TEXT",
		"ALTER TABLE workflows ADD COLUMN reconnect_interval TEXT",
		"ALTER TABLE workflows ADD COLUMN dry_run BOOLEAN DEFAULT FALSE",
		"ALTER TABLE workflows ADD COLUMN schema_type TEXT",
		"ALTER TABLE workflows ADD COLUMN schema TEXT",
		"ALTER TABLE workflows ADD COLUMN retention_days INTEGER",
		"ALTER TABLE workflows ADD COLUMN cron TEXT",
		"ALTER TABLE logs ADD COLUMN user_id TEXT",
		"ALTER TABLE logs ADD COLUMN username TEXT",
		"ALTER TABLE workflows ADD COLUMN idle_timeout TEXT",
		"ALTER TABLE workflows ADD COLUMN tier TEXT",
	}

	for _, q := range migrationQueries {
		// Ignore errors as the column might already exist
		_, _ = s.db.ExecContext(ctx, q)
	}

	// Indexes: Create indexes after columns are ensured to exist
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
		_, _ = s.db.ExecContext(ctx, q)
	}

	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create settings table: %w", err)
	}

	_, err = s.db.Exec(`
		CREATE TABLE IF NOT EXISTS audit_logs (
			id TEXT PRIMARY KEY,
			timestamp TIMESTAMP NOT NULL,
			user_id TEXT NOT NULL,
			username TEXT NOT NULL,
			action TEXT NOT NULL,
			entity_type TEXT NOT NULL,
			entity_id TEXT NOT NULL,
			payload TEXT,
			ip TEXT
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create audit_logs table: %w", err)
	}

	_, _ = s.db.ExecContext(ctx, "CREATE INDEX IF NOT EXISTS idx_audit_ts ON audit_logs(timestamp DESC)")
	_, _ = s.db.ExecContext(ctx, "CREATE INDEX IF NOT EXISTS idx_audit_user ON audit_logs(user_id)")
	_, _ = s.db.ExecContext(ctx, "CREATE INDEX IF NOT EXISTS idx_audit_entity ON audit_logs(entity_type, entity_id)")

	return nil
}

func (s *sqlStorage) ListSources(ctx context.Context, filter storage.CommonFilter) ([]storage.Source, int, error) {
	baseQuery := "SELECT id, name, type, vhost, active, status, worker_id, config, sample, state FROM sources"
	countQuery := "SELECT COUNT(*) FROM sources"
	var args []interface{}
	var where []string

	if filter.Search != "" {
		search := "%" + filter.Search + "%"
		where = append(where, "(id LIKE ? OR name LIKE ? OR type LIKE ? OR vhost LIKE ?)")
		args = append(args, search, search, search, search)
	}

	if filter.VHost != "" {
		where = append(where, "vhost = ?")
		args = append(args, filter.VHost)
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

	var sources []storage.Source
	for rows.Next() {
		var src storage.Source
		var status, workerID, configStr, sample, stateStr sql.NullString
		if err := rows.Scan(&src.ID, &src.Name, &src.Type, &src.VHost, &src.Active, &status, &workerID, &configStr, &sample, &stateStr); err != nil {
			return nil, 0, err
		}
		if status.Valid {
			src.Status = status.String
		}
		if workerID.Valid {
			src.WorkerID = workerID.String
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
		_, e := s.exec(ctx, "INSERT INTO sources (id, name, type, vhost, active, status, worker_id, config, sample, state) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
			src.ID, src.Name, src.Type, src.VHost, src.Active, src.Status, src.WorkerID, string(configBytes), src.Sample, string(stateBytes))
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
		_, e := s.exec(ctx, "UPDATE sources SET name = ?, type = ?, vhost = ?, active = ?, status = ?, worker_id = ?, config = ?, sample = ?, state = ? WHERE id = ?",
			src.Name, src.Type, src.VHost, src.Active, src.Status, src.WorkerID, string(configBytes), src.Sample, string(stateBytes), src.ID)
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
		_, e := s.exec(ctx, "UPDATE sources SET state = ? WHERE id = ?", string(stateBytes), id)
		return e
	}
	return s.execWithRetry(ctx, exec)
}

func (s *sqlStorage) DeleteSource(ctx context.Context, id string) error {
	exec := func() error {
		_, e := s.exec(ctx, "DELETE FROM sources WHERE id = ?", id)
		return e
	}
	return s.execWithRetry(ctx, exec)
}

func (s *sqlStorage) GetSource(ctx context.Context, id string) (storage.Source, error) {
	var src storage.Source
	var status, workerID, configStr, sample, stateStr sql.NullString
	err := s.queryRow(ctx, "SELECT id, name, type, vhost, active, status, worker_id, config, sample, state FROM sources WHERE id = ?", id).
		Scan(&src.ID, &src.Name, &src.Type, &src.VHost, &src.Active, &status, &workerID, &configStr, &sample, &stateStr)
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
	baseQuery := "SELECT id, name, type, vhost, active, status, worker_id, config FROM sinks"
	countQuery := "SELECT COUNT(*) FROM sinks"
	var args []interface{}
	var where []string

	if filter.Search != "" {
		search := "%" + filter.Search + "%"
		where = append(where, "(id LIKE ? OR name LIKE ? OR type LIKE ? OR vhost LIKE ?)")
		args = append(args, search, search, search, search)
	}

	if filter.VHost != "" {
		where = append(where, "vhost = ?")
		args = append(args, filter.VHost)
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

	var sinks []storage.Sink
	for rows.Next() {
		var snk storage.Sink
		var status, workerID, configStr sql.NullString
		if err := rows.Scan(&snk.ID, &snk.Name, &snk.Type, &snk.VHost, &snk.Active, &status, &workerID, &configStr); err != nil {
			return nil, 0, err
		}
		if status.Valid {
			snk.Status = status.String
		}
		if workerID.Valid {
			snk.WorkerID = workerID.String
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
		_, e := s.exec(ctx, "INSERT INTO sinks (id, name, type, vhost, active, status, worker_id, config) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
			snk.ID, snk.Name, snk.Type, snk.VHost, snk.Active, snk.Status, snk.WorkerID, string(configBytes))
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
		_, e := s.exec(ctx, "UPDATE sinks SET name = ?, type = ?, vhost = ?, active = ?, status = ?, worker_id = ?, config = ? WHERE id = ?",
			snk.Name, snk.Type, snk.VHost, snk.Active, snk.Status, snk.WorkerID, string(configBytes), snk.ID)
		return e
	}
	return s.execWithRetry(ctx, exec)
}

func (s *sqlStorage) DeleteSink(ctx context.Context, id string) error {
	exec := func() error {
		_, e := s.exec(ctx, "DELETE FROM sinks WHERE id = ?", id)
		return e
	}
	return s.execWithRetry(ctx, exec)
}

func (s *sqlStorage) GetSink(ctx context.Context, id string) (storage.Sink, error) {
	var snk storage.Sink
	var status, workerID, configStr sql.NullString
	err := s.queryRow(ctx, "SELECT id, name, type, vhost, active, status, worker_id, config FROM sinks WHERE id = ?", id).
		Scan(&snk.ID, &snk.Name, &snk.Type, &snk.VHost, &snk.Active, &status, &workerID, &configStr)
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
	baseQuery := "SELECT id, username, full_name, email, role, vhosts FROM users"
	countQuery := "SELECT COUNT(*) FROM users"
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

	var users []storage.User
	for rows.Next() {
		var user storage.User
		var vhostsStr string
		if err := rows.Scan(&user.ID, &user.Username, &user.FullName, &user.Email, &user.Role, &vhostsStr); err != nil {
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
	query := "INSERT INTO users (id, username, password, full_name, email, role, vhosts) VALUES (?, ?, ?, ?, ?, ?, ?)"
	_, err = s.exec(ctx, query,
		user.ID, user.Username, user.Password, user.FullName, user.Email, user.Role, string(vhostsBytes))
	return err
}

func (s *sqlStorage) UpdateUser(ctx context.Context, user storage.User) error {
	vhostsBytes, err := json.Marshal(user.VHosts)
	if err != nil {
		return err
	}
	if user.Password != "" {
		query := "UPDATE users SET username = ?, password = ?, full_name = ?, email = ?, role = ?, vhosts = ? WHERE id = ?"
		_, err = s.exec(ctx, query,
			user.Username, user.Password, user.FullName, user.Email, user.Role, string(vhostsBytes), user.ID)
	} else {
		query := "UPDATE users SET username = ?, full_name = ?, email = ?, role = ?, vhosts = ? WHERE id = ?"
		_, err = s.exec(ctx, query,
			user.Username, user.FullName, user.Email, user.Role, string(vhostsBytes), user.ID)
	}
	return err
}

func (s *sqlStorage) DeleteUser(ctx context.Context, id string) error {
	query := "DELETE FROM users WHERE id = ?"
	_, err := s.exec(ctx, query, id)
	return err
}

func (s *sqlStorage) GetUser(ctx context.Context, id string) (storage.User, error) {
	var user storage.User
	var vhostsStr string
	query := "SELECT id, username, full_name, email, role, vhosts FROM users WHERE id = ?"
	err := s.queryRow(ctx, query, id).
		Scan(&user.ID, &user.Username, &user.FullName, &user.Email, &user.Role, &vhostsStr)
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
	query := "SELECT id, username, password, full_name, email, role, vhosts FROM users WHERE username = ?"
	err := s.queryRow(ctx, query, username).
		Scan(&user.ID, &user.Username, &user.Password, &user.FullName, &user.Email, &user.Role, &vhostsStr)
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
	query := "SELECT id, username, password, full_name, email, role, vhosts FROM users WHERE email = ?"
	err := s.queryRow(ctx, query, email).
		Scan(&user.ID, &user.Username, &user.Password, &user.FullName, &user.Email, &user.Role, &vhostsStr)
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

func (s *sqlStorage) ListVHosts(ctx context.Context, filter storage.CommonFilter) ([]storage.VHost, int, error) {
	baseQuery := "SELECT id, name, description FROM vhosts"
	countQuery := "SELECT COUNT(*) FROM vhosts"
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

	var vhosts []storage.VHost
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
	_, err := s.exec(ctx, "INSERT INTO vhosts (id, name, description) VALUES (?, ?, ?)",
		vhost.ID, vhost.Name, vhost.Description)
	return err
}

func (s *sqlStorage) DeleteVHost(ctx context.Context, id string) error {
	_, err := s.exec(ctx, "DELETE FROM vhosts WHERE id = ?", id)
	return err
}

func (s *sqlStorage) GetVHost(ctx context.Context, id string) (storage.VHost, error) {
	var vhost storage.VHost
	err := s.queryRow(ctx, "SELECT id, name, description FROM vhosts WHERE id = ?", id).
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
	query := "SELECT id, name, vhost, active, status, worker_id, owner_id, lease_until, nodes, edges, dead_letter_sink_id, prioritize_dlq, max_retries, retry_interval, reconnect_interval, dry_run, schema_type, schema, retention_days, cron, idle_timeout, tier FROM workflows WHERE 1=1"
	args := []interface{}{}

	if filter.VHost != "" {
		query += " AND vhost = ?"
		args = append(args, filter.VHost)
	}

	if filter.Search != "" {
		query += " AND name LIKE ?"
		args = append(args, "%"+filter.Search+"%")
	}

	var total int
	countQuery := strings.Replace(query, "id, name, vhost, active, status, worker_id, owner_id, lease_until, nodes, edges, dead_letter_sink_id, prioritize_dlq, max_retries, retry_interval, reconnect_interval, dry_run, schema_type, schema, retention_days, cron, idle_timeout, tier", "COUNT(*)", 1)
	if err := s.queryRow(ctx, countQuery, args...).Scan(&total); err != nil {
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

	var wfs []storage.Workflow
	for rows.Next() {
		var wf storage.Workflow
		var nodesJSON, edgesJSON sql.NullString
		var leaseUntil sql.NullTime
		var ownerID sql.NullString
		var dlqSinkID, retryInterval, reconnectInterval sql.NullString
		var prioritizeDLQ, dryRun sql.NullBool
		var maxRetries, retentionDays sql.NullInt64
		var schemaType, schema, cron, idleTimeout, tier sql.NullString
		if err := rows.Scan(&wf.ID, &wf.Name, &wf.VHost, &wf.Active, &wf.Status, &wf.WorkerID, &ownerID, &leaseUntil, &nodesJSON, &edgesJSON, &dlqSinkID, &prioritizeDLQ, &maxRetries, &retryInterval, &reconnectInterval, &dryRun, &schemaType, &schema, &retentionDays, &cron, &idleTimeout, &tier); err != nil {
			return nil, 0, err
		}
		if dryRun.Valid {
			wf.DryRun = dryRun.Bool
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
		wfs = append(wfs, wf)
	}
	return wfs, total, nil
}

func (s *sqlStorage) CreateWorkflow(ctx context.Context, wf storage.Workflow) error {
	nodesJSON, _ := json.Marshal(wf.Nodes)
	edgesJSON, _ := json.Marshal(wf.Edges)
	if wf.ID == "" {
		wf.ID = uuid.New().String()
	}
	exec := func() error {
		_, e := s.exec(ctx,
			"INSERT INTO workflows (id, name, vhost, active, status, worker_id, nodes, edges, dead_letter_sink_id, prioritize_dlq, max_retries, retry_interval, reconnect_interval, dry_run, schema_type, schema, retention_days, cron, idle_timeout, tier) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
			wf.ID, wf.Name, wf.VHost, wf.Active, wf.Status, wf.WorkerID, string(nodesJSON), string(edgesJSON), wf.DeadLetterSinkID, wf.PrioritizeDLQ, wf.MaxRetries, wf.RetryInterval, wf.ReconnectInterval, wf.DryRun, wf.SchemaType, wf.Schema, wf.RetentionDays, wf.Cron, wf.IdleTimeout, string(wf.Tier),
		)
		return e
	}
	return s.execWithRetry(ctx, exec)
}

// UpdateWorkflow updates an existing workflow's metadata and topology (nodes/edges) by ID.
func (s *sqlStorage) UpdateWorkflow(ctx context.Context, wf storage.Workflow) error {
	nodesJSON, _ := json.Marshal(wf.Nodes)
	edgesJSON, _ := json.Marshal(wf.Edges)
	exec := func() error {
		_, e := s.exec(ctx,
			"UPDATE workflows SET name = ?, vhost = ?, active = ?, status = ?, worker_id = ?, nodes = ?, edges = ?, dead_letter_sink_id = ?, prioritize_dlq = ?, max_retries = ?, retry_interval = ?, reconnect_interval = ?, dry_run = ?, schema_type = ?, schema = ?, retention_days = ?, cron = ?, idle_timeout = ?, tier = ? WHERE id = ?",
			wf.Name, wf.VHost, wf.Active, wf.Status, wf.WorkerID, string(nodesJSON), string(edgesJSON), wf.DeadLetterSinkID, wf.PrioritizeDLQ, wf.MaxRetries, wf.RetryInterval, wf.ReconnectInterval, wf.DryRun, wf.SchemaType, wf.Schema, wf.RetentionDays, wf.Cron, wf.IdleTimeout, string(wf.Tier), wf.ID,
		)
		return e
	}
	return s.execWithRetry(ctx, exec)
}

func (s *sqlStorage) DeleteWorkflow(ctx context.Context, id string) error {
	exec := func() error {
		_, e := s.exec(ctx, "DELETE FROM workflows WHERE id = ?", id)
		return e
	}
	return s.execWithRetry(ctx, exec)
}

func (s *sqlStorage) GetWorkflow(ctx context.Context, id string) (storage.Workflow, error) {
	row := s.queryRow(ctx, "SELECT id, name, vhost, active, status, worker_id, owner_id, lease_until, nodes, edges, dead_letter_sink_id, prioritize_dlq, max_retries, retry_interval, reconnect_interval, dry_run, schema_type, schema, retention_days, cron, idle_timeout, tier FROM workflows WHERE id = ?", id)
	var wf storage.Workflow
	var nodesJSON, edgesJSON sql.NullString
	var leaseUntil sql.NullTime
	var ownerID sql.NullString
	var dlqSinkID, retryInterval, reconnectInterval sql.NullString
	var prioritizeDLQ, dryRun sql.NullBool
	var maxRetries, retentionDays sql.NullInt64
	var schemaType, schema, cron, idleTimeout, tier sql.NullString
	if err := row.Scan(&wf.ID, &wf.Name, &wf.VHost, &wf.Active, &wf.Status, &wf.WorkerID, &ownerID, &leaseUntil, &nodesJSON, &edgesJSON, &dlqSinkID, &prioritizeDLQ, &maxRetries, &retryInterval, &reconnectInterval, &dryRun, &schemaType, &schema, &retentionDays, &cron, &idleTimeout, &tier); err != nil {
		if err == sql.ErrNoRows {
			return storage.Workflow{}, storage.ErrNotFound
		}
		return storage.Workflow{}, err
	}
	if dryRun.Valid {
		wf.DryRun = dryRun.Bool
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
			"UPDATE workflows SET owner_id = ?, lease_until = ? WHERE id = ? AND (owner_id IS NULL OR lease_until IS NULL OR lease_until < ? OR owner_id = ?)",
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
			"UPDATE workflows SET lease_until = ? WHERE id = ? AND owner_id = ? AND lease_until IS NOT NULL AND lease_until >= ?",
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
			"UPDATE workflows SET owner_id = NULL, lease_until = NULL WHERE id = ? AND owner_id = ?",
			workflowID, ownerID,
		)
		return e
	}
	return s.execWithRetry(ctx, exec)
}

func (s *sqlStorage) ListWorkers(ctx context.Context, filter storage.CommonFilter) ([]storage.Worker, int, error) {
	baseQuery := "SELECT id, name, host, port, description, token, last_seen FROM workers"
	countQuery := "SELECT COUNT(*) FROM workers"
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

	var workers []storage.Worker
	for rows.Next() {
		var w storage.Worker
		var token sql.NullString
		var lastSeen sql.NullTime
		if err := rows.Scan(&w.ID, &w.Name, &w.Host, &w.Port, &w.Description, &token, &lastSeen); err != nil {
			return nil, 0, err
		}
		if token.Valid {
			w.Token = token.String
		}
		if lastSeen.Valid {
			w.LastSeen = &lastSeen.Time
		}
		workers = append(workers, w)
	}
	return workers, total, nil
}

func (s *sqlStorage) CreateWorker(ctx context.Context, worker storage.Worker) error {
	_, err := s.exec(ctx, "INSERT INTO workers (id, name, host, port, description, token, last_seen) VALUES (?, ?, ?, ?, ?, ?, ?)",
		worker.ID, worker.Name, worker.Host, worker.Port, worker.Description, worker.Token, worker.LastSeen)
	return err
}

func (s *sqlStorage) UpdateWorker(ctx context.Context, worker storage.Worker) error {
	_, err := s.exec(ctx, "UPDATE workers SET name = ?, host = ?, port = ?, description = ?, token = ?, last_seen = ? WHERE id = ?",
		worker.Name, worker.Host, worker.Port, worker.Description, worker.Token, worker.LastSeen, worker.ID)
	return err
}

func (s *sqlStorage) UpdateWorkerHeartbeat(ctx context.Context, id string) error {
	_, err := s.exec(ctx, "UPDATE workers SET last_seen = ? WHERE id = ?", time.Now(), id)
	return err
}

func (s *sqlStorage) DeleteWorker(ctx context.Context, id string) error {
	_, err := s.exec(ctx, "DELETE FROM workers WHERE id = ?", id)
	return err
}

func (s *sqlStorage) GetWorker(ctx context.Context, id string) (storage.Worker, error) {
	var w storage.Worker
	var token sql.NullString
	var lastSeen sql.NullTime
	err := s.queryRow(ctx, "SELECT id, name, host, port, description, token, last_seen FROM workers WHERE id = ?", id).
		Scan(&w.ID, &w.Name, &w.Host, &w.Port, &w.Description, &token, &lastSeen)
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
	return w, nil
}

func (s *sqlStorage) ListLogs(ctx context.Context, filter storage.LogFilter) ([]storage.Log, int, error) {
	query := " FROM logs WHERE 1=1"
	var args []interface{}

	// Time bounds (if provided)
	if !filter.Since.IsZero() {
		query += " AND timestamp >= ?"
		args = append(args, filter.Since)
	}
	if !filter.Until.IsZero() {
		query += " AND timestamp < ?"
		args = append(args, filter.Until)
	}

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
	if filter.Search != "" {
		search := "%" + filter.Search + "%"
		// Avoid scanning large 'data' payloads for LIKE to improve performance.
		// Search in message and identifiers only.
		query += " AND (message LIKE ? OR action LIKE ? OR source_id LIKE ? OR sink_id LIKE ? OR workflow_id LIKE ?)"
		args = append(args, search, search, search, search, search)
	}

	var total int
	if err := s.queryRow(ctx, "SELECT COUNT(*)"+query, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	query += " ORDER BY timestamp DESC"

	if filter.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, filter.Limit)
		if filter.Page > 0 {
			query += " OFFSET ?"
			args = append(args, (filter.Page-1)*filter.Limit)
		}
	} else {
		query += " LIMIT 100"
	}

	rows, err := s.query(ctx, "SELECT id, timestamp, level, message, action, source_id, sink_id, workflow_id, user_id, username, data"+query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var logs []storage.Log
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
		_, e := s.exec(ctx, "INSERT INTO logs (id, timestamp, level, message, action, source_id, sink_id, workflow_id, user_id, username, data) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
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
	query := "DELETE FROM logs WHERE 1=1"
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
	err := s.queryRow(ctx, "SELECT value FROM settings WHERE key = ?", key).Scan(&value)
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

	var query string
	switch s.driver {
	case "mysql", "mariadb":
		query = "INSERT INTO workflow_node_states (workflow_id, node_id, state) VALUES (?, ?, ?) ON DUPLICATE KEY UPDATE state = VALUES(state)"
	case "pgx":
		query = "INSERT INTO workflow_node_states (workflow_id, node_id, state) VALUES ($1, $2, $3) ON CONFLICT(workflow_id, node_id) DO UPDATE SET state = excluded.state"
	default:
		query = "INSERT INTO workflow_node_states (workflow_id, node_id, state) VALUES (?, ?, ?) ON CONFLICT(workflow_id, node_id) DO UPDATE SET state = excluded.state"
	}

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
	var query string
	switch s.driver {
	case "mysql", "mariadb":
		query = "INSERT INTO settings (`key`, value) VALUES (?, ?) ON DUPLICATE KEY UPDATE value = VALUES(value)"
	case "pgx":
		query = "INSERT INTO settings (key, value) VALUES ($1, $2) ON CONFLICT(key) DO UPDATE SET value = excluded.value"
	case "sqlserver":
		query = "MERGE settings WITH (HOLDLOCK) AS t USING (SELECT @p1 AS [key], @p2 AS value) AS s ON t.[key] = s.[key] WHEN MATCHED THEN UPDATE SET value = s.value WHEN NOT MATCHED THEN INSERT([key], value) VALUES(s.[key], s.value);"
	default:
		query = "INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value"
	}
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
		_, err := s.exec(ctx, "INSERT INTO audit_logs (id, timestamp, user_id, username, action, entity_type, entity_id, payload, ip) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
			log.ID, log.Timestamp, log.UserID, log.Username, log.Action, log.EntityType, log.EntityID, log.Payload, log.IP)
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
		_, err := s.exec(ctx, "INSERT INTO webhook_requests (id, timestamp, path, method, headers, body) VALUES (?, ?, ?, ?, ?, ?)",
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

	var requests []storage.WebhookRequest
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
	query := "SELECT id, timestamp, path, method, headers, body FROM webhook_requests WHERE id = ?"
	var req storage.WebhookRequest
	var headersJSON string
	err := s.queryRow(ctx, query, id).Scan(&req.ID, &req.Timestamp, &req.Path, &req.Method, &headersJSON, &req.Body)
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

	var logs []storage.AuditLog
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
