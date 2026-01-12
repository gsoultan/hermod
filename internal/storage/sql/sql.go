package sql

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
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
	db *sql.DB
}

func NewSQLStorage(db *sql.DB) storage.Storage {
	return &sqlStorage{db: db}
}

func (s *sqlStorage) Init(ctx context.Context) error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS sources (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			type TEXT,
			vhost TEXT,
			active BOOLEAN DEFAULT 0,
			worker_id TEXT,
			config TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS sinks (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			type TEXT,
			vhost TEXT,
			active BOOLEAN DEFAULT 0,
			worker_id TEXT,
			config TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS connections (
			id TEXT PRIMARY KEY,
			name TEXT,
			vhost TEXT,
			source_id TEXT,
			sink_ids TEXT,
			active BOOLEAN,
			worker_id TEXT,
			transformation_ids TEXT,
			transformations TEXT
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
			token TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS logs (
			id TEXT PRIMARY KEY,
			timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
			level TEXT,
			message TEXT,
			action TEXT,
			source_id TEXT,
			sink_id TEXT,
			connection_id TEXT,
			data TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS transformations (
			id TEXT PRIMARY KEY,
			name TEXT,
			type TEXT,
			steps TEXT,
			config TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_logs_connection_id ON logs(connection_id)`,
		`CREATE INDEX IF NOT EXISTS idx_logs_source_id ON logs(source_id)`,
		`CREATE INDEX IF NOT EXISTS idx_logs_sink_id ON logs(sink_id)`,
		`CREATE INDEX IF NOT EXISTS idx_logs_timestamp ON logs(timestamp)`,
	}

	for _, q := range queries {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("failed to execute init query: %w", err)
		}
	}

	// Migrations: Add new columns if missing
	migrationQueries := []string{
		"ALTER TABLE sources ADD COLUMN transformations TEXT",
		"ALTER TABLE sinks ADD COLUMN transformations TEXT",
		"ALTER TABLE sources ADD COLUMN worker_id TEXT",
		"ALTER TABLE sinks ADD COLUMN worker_id TEXT",
		"ALTER TABLE connections ADD COLUMN worker_id TEXT",
		"ALTER TABLE connections ADD COLUMN transformations TEXT",
		"ALTER TABLE connections ADD COLUMN transformation_ids TEXT",
		"ALTER TABLE workers ADD COLUMN token TEXT",
		"ALTER TABLE sources ADD COLUMN active BOOLEAN DEFAULT 0",
		"ALTER TABLE sinks ADD COLUMN active BOOLEAN DEFAULT 0",
		"ALTER TABLE logs ADD COLUMN action TEXT",
	}

	for _, q := range migrationQueries {
		// Ignore errors as the column might already exist
		_, _ = s.db.ExecContext(ctx, q)
	}

	return nil
}

func (s *sqlStorage) ListSources(ctx context.Context) ([]storage.Source, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, name, type, vhost, active, worker_id, config FROM sources")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sources []storage.Source
	for rows.Next() {
		var src storage.Source
		var workerID, configStr sql.NullString
		if err := rows.Scan(&src.ID, &src.Name, &src.Type, &src.VHost, &src.Active, &workerID, &configStr); err != nil {
			return nil, err
		}
		if workerID.Valid {
			src.WorkerID = workerID.String
		}
		if configStr.Valid {
			if err := json.Unmarshal([]byte(configStr.String), &src.Config); err != nil {
				return nil, err
			}
			src.Config = decryptConfig(src.Config)
		}
		sources = append(sources, src)
	}
	return sources, nil
}

func (s *sqlStorage) CreateSource(ctx context.Context, src storage.Source) error {
	configBytes, err := json.Marshal(encryptConfig(src.Config))
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "INSERT INTO sources (id, name, type, vhost, active, worker_id, config) VALUES (?, ?, ?, ?, ?, ?, ?)",
		src.ID, src.Name, src.Type, src.VHost, src.Active, src.WorkerID, string(configBytes))
	return err
}

func (s *sqlStorage) UpdateSource(ctx context.Context, src storage.Source) error {
	configBytes, err := json.Marshal(encryptConfig(src.Config))
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "UPDATE sources SET name = ?, type = ?, vhost = ?, active = ?, worker_id = ?, config = ? WHERE id = ?",
		src.Name, src.Type, src.VHost, src.Active, src.WorkerID, string(configBytes), src.ID)
	return err
}

func (s *sqlStorage) DeleteSource(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM sources WHERE id = ?", id)
	return err
}

func (s *sqlStorage) GetSource(ctx context.Context, id string) (storage.Source, error) {
	var src storage.Source
	var workerID, configStr sql.NullString
	err := s.db.QueryRowContext(ctx, "SELECT id, name, type, vhost, active, worker_id, config FROM sources WHERE id = ?", id).
		Scan(&src.ID, &src.Name, &src.Type, &src.VHost, &src.Active, &workerID, &configStr)
	if err != nil {
		return storage.Source{}, err
	}
	if workerID.Valid {
		src.WorkerID = workerID.String
	}
	if configStr.Valid {
		if err := json.Unmarshal([]byte(configStr.String), &src.Config); err != nil {
			return storage.Source{}, err
		}
		src.Config = decryptConfig(src.Config)
	}
	return src, nil
}

func (s *sqlStorage) ListSinks(ctx context.Context) ([]storage.Sink, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, name, type, vhost, active, worker_id, config FROM sinks")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sinks []storage.Sink
	for rows.Next() {
		var snk storage.Sink
		var workerID, configStr sql.NullString
		if err := rows.Scan(&snk.ID, &snk.Name, &snk.Type, &snk.VHost, &snk.Active, &workerID, &configStr); err != nil {
			return nil, err
		}
		if workerID.Valid {
			snk.WorkerID = workerID.String
		}
		if configStr.Valid {
			if err := json.Unmarshal([]byte(configStr.String), &snk.Config); err != nil {
				return nil, err
			}
			snk.Config = decryptConfig(snk.Config)
		}
		sinks = append(sinks, snk)
	}
	return sinks, nil
}

func (s *sqlStorage) CreateSink(ctx context.Context, snk storage.Sink) error {
	configBytes, err := json.Marshal(encryptConfig(snk.Config))
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "INSERT INTO sinks (id, name, type, vhost, active, worker_id, config) VALUES (?, ?, ?, ?, ?, ?, ?)",
		snk.ID, snk.Name, snk.Type, snk.VHost, snk.Active, snk.WorkerID, string(configBytes))
	return err
}

func (s *sqlStorage) UpdateSink(ctx context.Context, snk storage.Sink) error {
	configBytes, err := json.Marshal(encryptConfig(snk.Config))
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "UPDATE sinks SET name = ?, type = ?, vhost = ?, active = ?, worker_id = ?, config = ? WHERE id = ?",
		snk.Name, snk.Type, snk.VHost, snk.Active, snk.WorkerID, string(configBytes), snk.ID)
	return err
}

func (s *sqlStorage) DeleteSink(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM sinks WHERE id = ?", id)
	return err
}

func (s *sqlStorage) GetSink(ctx context.Context, id string) (storage.Sink, error) {
	var snk storage.Sink
	var workerID, configStr sql.NullString
	err := s.db.QueryRowContext(ctx, "SELECT id, name, type, vhost, active, worker_id, config FROM sinks WHERE id = ?", id).
		Scan(&snk.ID, &snk.Name, &snk.Type, &snk.VHost, &snk.Active, &workerID, &configStr)
	if err != nil {
		return storage.Sink{}, err
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

func (s *sqlStorage) ListConnections(ctx context.Context) ([]storage.Connection, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, name, vhost, source_id, sink_ids, active, worker_id, transformation_ids, transformations FROM connections")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var connections []storage.Connection
	for rows.Next() {
		var conn storage.Connection
		var sinkIDsStr, workerID, transIDsStr, transStr sql.NullString
		if err := rows.Scan(&conn.ID, &conn.Name, &conn.VHost, &conn.SourceID, &sinkIDsStr, &conn.Active, &workerID, &transIDsStr, &transStr); err != nil {
			return nil, err
		}
		if workerID.Valid {
			conn.WorkerID = workerID.String
		}
		if sinkIDsStr.Valid {
			if err := json.Unmarshal([]byte(sinkIDsStr.String), &conn.SinkIDs); err != nil {
				return nil, err
			}
		}
		if transIDsStr.Valid && transIDsStr.String != "" {
			json.Unmarshal([]byte(transIDsStr.String), &conn.TransformationIDs)
		}
		if transStr.Valid && transStr.String != "" {
			json.Unmarshal([]byte(transStr.String), &conn.Transformations)
		}
		connections = append(connections, conn)
	}
	return connections, nil
}

func (s *sqlStorage) CreateConnection(ctx context.Context, conn storage.Connection) error {
	sinkIDsBytes, err := json.Marshal(conn.SinkIDs)
	if err != nil {
		return err
	}
	transIDsBytes, _ := json.Marshal(conn.TransformationIDs)
	transBytes, _ := json.Marshal(conn.Transformations)
	_, err = s.db.ExecContext(ctx, "INSERT INTO connections (id, name, vhost, source_id, sink_ids, active, worker_id, transformation_ids, transformations) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		conn.ID, conn.Name, conn.VHost, conn.SourceID, string(sinkIDsBytes), conn.Active, conn.WorkerID, string(transIDsBytes), string(transBytes))
	return err
}

func (s *sqlStorage) UpdateConnection(ctx context.Context, conn storage.Connection) error {
	sinkIDsBytes, err := json.Marshal(conn.SinkIDs)
	if err != nil {
		return err
	}
	transIDsBytes, _ := json.Marshal(conn.TransformationIDs)
	transBytes, _ := json.Marshal(conn.Transformations)
	_, err = s.db.ExecContext(ctx, "UPDATE connections SET name = ?, vhost = ?, source_id = ?, sink_ids = ?, active = ?, worker_id = ?, transformation_ids = ?, transformations = ? WHERE id = ?",
		conn.Name, conn.VHost, conn.SourceID, string(sinkIDsBytes), conn.Active, conn.WorkerID, string(transIDsBytes), string(transBytes), conn.ID)
	return err
}

func (s *sqlStorage) DeleteConnection(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM connections WHERE id = ?", id)
	return err
}

func (s *sqlStorage) GetConnection(ctx context.Context, id string) (storage.Connection, error) {
	var conn storage.Connection
	var sinkIDsStr, workerID, transIDsStr, transStr sql.NullString
	err := s.db.QueryRowContext(ctx, "SELECT id, name, vhost, source_id, sink_ids, active, worker_id, transformation_ids, transformations FROM connections WHERE id = ?", id).
		Scan(&conn.ID, &conn.Name, &conn.VHost, &conn.SourceID, &sinkIDsStr, &conn.Active, &workerID, &transIDsStr, &transStr)
	if err != nil {
		return storage.Connection{}, err
	}
	if workerID.Valid {
		conn.WorkerID = workerID.String
	}
	if sinkIDsStr.Valid {
		if err := json.Unmarshal([]byte(sinkIDsStr.String), &conn.SinkIDs); err != nil {
			return storage.Connection{}, err
		}
	}
	if transIDsStr.Valid && transIDsStr.String != "" {
		json.Unmarshal([]byte(transIDsStr.String), &conn.TransformationIDs)
	}
	if transStr.Valid && transStr.String != "" {
		json.Unmarshal([]byte(transStr.String), &conn.Transformations)
	}
	return conn, nil
}

func (s *sqlStorage) ListUsers(ctx context.Context) ([]storage.User, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, username, full_name, email, role, vhosts FROM users")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []storage.User
	for rows.Next() {
		var user storage.User
		var vhostsStr string
		if err := rows.Scan(&user.ID, &user.Username, &user.FullName, &user.Email, &user.Role, &vhostsStr); err != nil {
			return nil, err
		}
		if vhostsStr != "" {
			if err := json.Unmarshal([]byte(vhostsStr), &user.VHosts); err != nil {
				return nil, err
			}
		}
		users = append(users, user)
	}
	return users, nil
}

func (s *sqlStorage) CreateUser(ctx context.Context, user storage.User) error {
	vhostsBytes, err := json.Marshal(user.VHosts)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "INSERT INTO users (id, username, password, full_name, email, role, vhosts) VALUES (?, ?, ?, ?, ?, ?, ?)",
		user.ID, user.Username, user.Password, user.FullName, user.Email, user.Role, string(vhostsBytes))
	return err
}

func (s *sqlStorage) UpdateUser(ctx context.Context, user storage.User) error {
	vhostsBytes, err := json.Marshal(user.VHosts)
	if err != nil {
		return err
	}
	if user.Password != "" {
		_, err = s.db.ExecContext(ctx, "UPDATE users SET username = ?, password = ?, full_name = ?, email = ?, role = ?, vhosts = ? WHERE id = ?",
			user.Username, user.Password, user.FullName, user.Email, user.Role, string(vhostsBytes), user.ID)
	} else {
		_, err = s.db.ExecContext(ctx, "UPDATE users SET username = ?, full_name = ?, email = ?, role = ?, vhosts = ? WHERE id = ?",
			user.Username, user.FullName, user.Email, user.Role, string(vhostsBytes), user.ID)
	}
	return err
}

func (s *sqlStorage) DeleteUser(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM users WHERE id = ?", id)
	return err
}

func (s *sqlStorage) GetUser(ctx context.Context, id string) (storage.User, error) {
	var user storage.User
	var vhostsStr string
	err := s.db.QueryRowContext(ctx, "SELECT id, username, full_name, email, role, vhosts FROM users WHERE id = ?", id).
		Scan(&user.ID, &user.Username, &user.FullName, &user.Email, &user.Role, &vhostsStr)
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
	err := s.db.QueryRowContext(ctx, "SELECT id, username, password, full_name, email, role, vhosts FROM users WHERE username = ?", username).
		Scan(&user.ID, &user.Username, &user.Password, &user.FullName, &user.Email, &user.Role, &vhostsStr)
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

func (s *sqlStorage) ListVHosts(ctx context.Context) ([]storage.VHost, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, name, description FROM vhosts")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var vhosts []storage.VHost
	for rows.Next() {
		var vhost storage.VHost
		if err := rows.Scan(&vhost.ID, &vhost.Name, &vhost.Description); err != nil {
			return nil, err
		}
		vhosts = append(vhosts, vhost)
	}
	return vhosts, nil
}

func (s *sqlStorage) CreateVHost(ctx context.Context, vhost storage.VHost) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO vhosts (id, name, description) VALUES (?, ?, ?)",
		vhost.ID, vhost.Name, vhost.Description)
	return err
}

func (s *sqlStorage) DeleteVHost(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM vhosts WHERE id = ?", id)
	return err
}

func (s *sqlStorage) GetVHost(ctx context.Context, id string) (storage.VHost, error) {
	var vhost storage.VHost
	err := s.db.QueryRowContext(ctx, "SELECT id, name, description FROM vhosts WHERE id = ?", id).
		Scan(&vhost.ID, &vhost.Name, &vhost.Description)
	if err != nil {
		return storage.VHost{}, err
	}
	return vhost, nil
}

func (s *sqlStorage) ListTransformations(ctx context.Context) ([]storage.Transformation, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, name, type, steps, config FROM transformations")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var transformations []storage.Transformation
	for rows.Next() {
		var trans storage.Transformation
		var stepsStr, configStr sql.NullString
		if err := rows.Scan(&trans.ID, &trans.Name, &trans.Type, &stepsStr, &configStr); err != nil {
			return nil, err
		}

		if stepsStr.Valid {
			_ = json.Unmarshal([]byte(stepsStr.String), &trans.Steps)
		}
		if configStr.Valid {
			_ = json.Unmarshal([]byte(configStr.String), &trans.Config)
		}
		transformations = append(transformations, trans)
	}
	return transformations, nil
}

func (s *sqlStorage) CreateTransformation(ctx context.Context, trans storage.Transformation) error {
	stepsJSON, _ := json.Marshal(trans.Steps)
	configJSON, _ := json.Marshal(trans.Config)
	_, err := s.db.ExecContext(ctx, "INSERT INTO transformations (id, name, type, steps, config) VALUES (?, ?, ?, ?, ?)",
		trans.ID, trans.Name, trans.Type, string(stepsJSON), string(configJSON))
	return err
}

func (s *sqlStorage) UpdateTransformation(ctx context.Context, trans storage.Transformation) error {
	stepsJSON, _ := json.Marshal(trans.Steps)
	configJSON, _ := json.Marshal(trans.Config)
	_, err := s.db.ExecContext(ctx, "UPDATE transformations SET name = ?, type = ?, steps = ?, config = ? WHERE id = ?",
		trans.Name, trans.Type, string(stepsJSON), string(configJSON), trans.ID)
	return err
}

func (s *sqlStorage) DeleteTransformation(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM transformations WHERE id = ?", id)
	return err
}

func (s *sqlStorage) GetTransformation(ctx context.Context, id string) (storage.Transformation, error) {
	var trans storage.Transformation
	var stepsStr, configStr sql.NullString
	err := s.db.QueryRowContext(ctx, "SELECT id, name, type, steps, config FROM transformations WHERE id = ?", id).
		Scan(&trans.ID, &trans.Name, &trans.Type, &stepsStr, &configStr)
	if err == sql.ErrNoRows {
		return trans, storage.ErrNotFound
	}
	if err != nil {
		return trans, err
	}

	if stepsStr.Valid {
		_ = json.Unmarshal([]byte(stepsStr.String), &trans.Steps)
	}
	if configStr.Valid {
		_ = json.Unmarshal([]byte(configStr.String), &trans.Config)
	}
	return trans, nil
}

func (s *sqlStorage) ListWorkers(ctx context.Context) ([]storage.Worker, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, name, host, port, description, token FROM workers")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var workers []storage.Worker
	for rows.Next() {
		var w storage.Worker
		var token sql.NullString
		if err := rows.Scan(&w.ID, &w.Name, &w.Host, &w.Port, &w.Description, &token); err != nil {
			return nil, err
		}
		if token.Valid {
			w.Token = token.String
		}
		workers = append(workers, w)
	}
	return workers, nil
}

func (s *sqlStorage) CreateWorker(ctx context.Context, worker storage.Worker) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO workers (id, name, host, port, description, token) VALUES (?, ?, ?, ?, ?, ?)",
		worker.ID, worker.Name, worker.Host, worker.Port, worker.Description, worker.Token)
	return err
}

func (s *sqlStorage) UpdateWorker(ctx context.Context, worker storage.Worker) error {
	_, err := s.db.ExecContext(ctx, "UPDATE workers SET name = ?, host = ?, port = ?, description = ?, token = ? WHERE id = ?",
		worker.Name, worker.Host, worker.Port, worker.Description, worker.Token, worker.ID)
	return err
}

func (s *sqlStorage) DeleteWorker(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM workers WHERE id = ?", id)
	return err
}

func (s *sqlStorage) GetWorker(ctx context.Context, id string) (storage.Worker, error) {
	var w storage.Worker
	var token sql.NullString
	err := s.db.QueryRowContext(ctx, "SELECT id, name, host, port, description, token FROM workers WHERE id = ?", id).
		Scan(&w.ID, &w.Name, &w.Host, &w.Port, &w.Description, &token)
	if token.Valid {
		w.Token = token.String
	}
	return w, err
}

func (s *sqlStorage) ListLogs(ctx context.Context, filter storage.LogFilter) ([]storage.Log, error) {
	query := "SELECT id, timestamp, level, message, action, source_id, sink_id, connection_id, data FROM logs WHERE 1=1"
	var args []interface{}

	if filter.SourceID != "" {
		query += " AND source_id = ?"
		args = append(args, filter.SourceID)
	}
	if filter.SinkID != "" {
		query += " AND sink_id = ?"
		args = append(args, filter.SinkID)
	}
	if filter.ConnectionID != "" {
		query += " AND connection_id = ?"
		args = append(args, filter.ConnectionID)
	}
	if filter.Level != "" {
		query += " AND level = ?"
		args = append(args, filter.Level)
	}
	if filter.Action != "" {
		query += " AND action = ?"
		args = append(args, filter.Action)
	}

	query += " ORDER BY timestamp DESC"

	if filter.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, filter.Limit)
	} else {
		query += " LIMIT 100"
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []storage.Log
	for rows.Next() {
		var l storage.Log
		var action, sourceID, sinkID, connectionID, data sql.NullString
		if err := rows.Scan(&l.ID, &l.Timestamp, &l.Level, &l.Message, &action, &sourceID, &sinkID, &connectionID, &data); err != nil {
			return nil, err
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
		if connectionID.Valid {
			l.ConnectionID = connectionID.String
		}
		if data.Valid {
			l.Data = data.String
		}
		logs = append(logs, l)
	}
	return logs, nil
}

func (s *sqlStorage) CreateLog(ctx context.Context, l storage.Log) error {
	if l.ID == "" {
		l.ID = uuid.New().String()
	}
	if l.Timestamp.IsZero() {
		l.Timestamp = time.Now()
	}

	_, err := s.db.ExecContext(ctx, "INSERT INTO logs (id, timestamp, level, message, action, source_id, sink_id, connection_id, data) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		l.ID, l.Timestamp, l.Level, l.Message,
		sql.NullString{String: l.Action, Valid: l.Action != ""},
		sql.NullString{String: l.SourceID, Valid: l.SourceID != ""},
		sql.NullString{String: l.SinkID, Valid: l.SinkID != ""},
		sql.NullString{String: l.ConnectionID, Valid: l.ConnectionID != ""},
		sql.NullString{String: l.Data, Valid: l.Data != ""},
	)
	return err
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
	if filter.ConnectionID != "" {
		query += " AND connection_id = ?"
		args = append(args, filter.ConnectionID)
	}
	if filter.Level != "" {
		query += " AND level = ?"
		args = append(args, filter.Level)
	}
	if filter.Action != "" {
		query += " AND action = ?"
		args = append(args, filter.Action)
	}

	_, err := s.db.ExecContext(ctx, query, args...)
	return err
}
