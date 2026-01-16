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
			status TEXT,
			worker_id TEXT,
			config TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS sinks (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			type TEXT,
			vhost TEXT,
			active BOOLEAN DEFAULT 0,
			status TEXT,
			worker_id TEXT,
			config TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS connections (
			id TEXT PRIMARY KEY,
			name TEXT,
			vhost TEXT,
			source_id TEXT,
			sink_ids TEXT,
			transformation_groups TEXT,
			active BOOLEAN,
			status TEXT,
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
			token TEXT,
			last_seen DATETIME
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
			config TEXT,
			on_failure TEXT,
			execute_if TEXT
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
		"ALTER TABLE connections ADD COLUMN transformation_groups TEXT",
		"ALTER TABLE connections ADD COLUMN status TEXT",
		"ALTER TABLE workers ADD COLUMN token TEXT",
		"ALTER TABLE workers ADD COLUMN last_seen DATETIME",
		"ALTER TABLE sources ADD COLUMN active BOOLEAN DEFAULT 0",
		"ALTER TABLE sinks ADD COLUMN active BOOLEAN DEFAULT 0",
		"ALTER TABLE logs ADD COLUMN action TEXT",
		"ALTER TABLE transformations ADD COLUMN on_failure TEXT",
		"ALTER TABLE transformations ADD COLUMN execute_if TEXT",
		"ALTER TABLE sources ADD COLUMN status TEXT",
		"ALTER TABLE sinks ADD COLUMN status TEXT",
	}

	for _, q := range migrationQueries {
		// Ignore errors as the column might already exist
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

	return nil
}

func (s *sqlStorage) ListSources(ctx context.Context, filter storage.CommonFilter) ([]storage.Source, int, error) {
	baseQuery := "SELECT id, name, type, vhost, active, status, worker_id, config FROM sources"
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
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
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

	rows, err := s.db.QueryContext(ctx, baseQuery, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var sources []storage.Source
	for rows.Next() {
		var src storage.Source
		var status, workerID, configStr sql.NullString
		if err := rows.Scan(&src.ID, &src.Name, &src.Type, &src.VHost, &src.Active, &status, &workerID, &configStr); err != nil {
			return nil, 0, err
		}
		if status.Valid {
			src.Status = status.String
		}
		if workerID.Valid {
			src.WorkerID = workerID.String
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
	_, err = s.db.ExecContext(ctx, "INSERT INTO sources (id, name, type, vhost, active, status, worker_id, config) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		src.ID, src.Name, src.Type, src.VHost, src.Active, src.Status, src.WorkerID, string(configBytes))
	return err
}

func (s *sqlStorage) UpdateSource(ctx context.Context, src storage.Source) error {
	configBytes, err := json.Marshal(encryptConfig(src.Config))
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "UPDATE sources SET name = ?, type = ?, vhost = ?, active = ?, status = ?, worker_id = ?, config = ? WHERE id = ?",
		src.Name, src.Type, src.VHost, src.Active, src.Status, src.WorkerID, string(configBytes), src.ID)
	return err
}

func (s *sqlStorage) DeleteSource(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM sources WHERE id = ?", id)
	return err
}

func (s *sqlStorage) GetSource(ctx context.Context, id string) (storage.Source, error) {
	var src storage.Source
	var status, workerID, configStr sql.NullString
	err := s.db.QueryRowContext(ctx, "SELECT id, name, type, vhost, active, status, worker_id, config FROM sources WHERE id = ?", id).
		Scan(&src.ID, &src.Name, &src.Type, &src.VHost, &src.Active, &status, &workerID, &configStr)
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
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
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

	rows, err := s.db.QueryContext(ctx, baseQuery, args...)
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
	_, err = s.db.ExecContext(ctx, "INSERT INTO sinks (id, name, type, vhost, active, status, worker_id, config) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		snk.ID, snk.Name, snk.Type, snk.VHost, snk.Active, snk.Status, snk.WorkerID, string(configBytes))
	return err
}

func (s *sqlStorage) UpdateSink(ctx context.Context, snk storage.Sink) error {
	configBytes, err := json.Marshal(encryptConfig(snk.Config))
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "UPDATE sinks SET name = ?, type = ?, vhost = ?, active = ?, status = ?, worker_id = ?, config = ? WHERE id = ?",
		snk.Name, snk.Type, snk.VHost, snk.Active, snk.Status, snk.WorkerID, string(configBytes), snk.ID)
	return err
}

func (s *sqlStorage) DeleteSink(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM sinks WHERE id = ?", id)
	return err
}

func (s *sqlStorage) GetSink(ctx context.Context, id string) (storage.Sink, error) {
	var snk storage.Sink
	var status, workerID, configStr sql.NullString
	err := s.db.QueryRowContext(ctx, "SELECT id, name, type, vhost, active, status, worker_id, config FROM sinks WHERE id = ?", id).
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

func (s *sqlStorage) ListConnections(ctx context.Context, filter storage.CommonFilter) ([]storage.Connection, int, error) {
	baseQuery := "SELECT id, name, vhost, source_id, sink_ids, transformation_groups, active, status, worker_id, transformation_ids, transformations FROM connections"
	countQuery := "SELECT COUNT(*) FROM connections"
	var args []interface{}
	var where []string

	if filter.Search != "" {
		search := "%" + filter.Search + "%"
		where = append(where, "(id LIKE ? OR name LIKE ? OR vhost LIKE ? OR source_id LIKE ? OR sink_ids LIKE ? OR status LIKE ?)")
		args = append(args, search, search, search, search, search, search)
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
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
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

	rows, err := s.db.QueryContext(ctx, baseQuery, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var connections []storage.Connection
	for rows.Next() {
		var conn storage.Connection
		var sinkIDsStr, transGroupsStr, status, workerID, transIDsStr, transStr sql.NullString
		if err := rows.Scan(&conn.ID, &conn.Name, &conn.VHost, &conn.SourceID, &sinkIDsStr, &transGroupsStr, &conn.Active, &status, &workerID, &transIDsStr, &transStr); err != nil {
			return nil, 0, err
		}
		if status.Valid {
			conn.Status = status.String
		}
		if workerID.Valid {
			conn.WorkerID = workerID.String
		}
		if sinkIDsStr.Valid {
			if err := json.Unmarshal([]byte(sinkIDsStr.String), &conn.SinkIDs); err != nil {
				return nil, 0, err
			}
		}
		if transGroupsStr.Valid && transGroupsStr.String != "" {
			json.Unmarshal([]byte(transGroupsStr.String), &conn.TransformationGroups)
		}
		if transIDsStr.Valid && transIDsStr.String != "" {
			json.Unmarshal([]byte(transIDsStr.String), &conn.TransformationIDs)
		}
		if transStr.Valid && transStr.String != "" {
			json.Unmarshal([]byte(transStr.String), &conn.Transformations)
		}
		connections = append(connections, conn)
	}
	return connections, total, nil
}

func (s *sqlStorage) CreateConnection(ctx context.Context, conn storage.Connection) error {
	sinkIDsBytes, err := json.Marshal(conn.SinkIDs)
	if err != nil {
		return err
	}
	transGroupsBytes, _ := json.Marshal(conn.TransformationGroups)
	transIDsBytes, _ := json.Marshal(conn.TransformationIDs)
	transBytes, _ := json.Marshal(conn.Transformations)
	_, err = s.db.ExecContext(ctx, "INSERT INTO connections (id, name, vhost, source_id, sink_ids, transformation_groups, active, status, worker_id, transformation_ids, transformations) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		conn.ID, conn.Name, conn.VHost, conn.SourceID, string(sinkIDsBytes), string(transGroupsBytes), conn.Active, conn.Status, conn.WorkerID, string(transIDsBytes), string(transBytes))
	return err
}

func (s *sqlStorage) UpdateConnection(ctx context.Context, conn storage.Connection) error {
	sinkIDsBytes, err := json.Marshal(conn.SinkIDs)
	if err != nil {
		return err
	}
	transGroupsBytes, _ := json.Marshal(conn.TransformationGroups)
	transIDsBytes, _ := json.Marshal(conn.TransformationIDs)
	transBytes, _ := json.Marshal(conn.Transformations)
	_, err = s.db.ExecContext(ctx, "UPDATE connections SET name = ?, vhost = ?, source_id = ?, sink_ids = ?, transformation_groups = ?, active = ?, status = ?, worker_id = ?, transformation_ids = ?, transformations = ? WHERE id = ?",
		conn.Name, conn.VHost, conn.SourceID, string(sinkIDsBytes), string(transGroupsBytes), conn.Active, conn.Status, conn.WorkerID, string(transIDsBytes), string(transBytes), conn.ID)
	return err
}

func (s *sqlStorage) DeleteConnection(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM connections WHERE id = ?", id)
	return err
}

func (s *sqlStorage) GetConnection(ctx context.Context, id string) (storage.Connection, error) {
	var conn storage.Connection
	var sinkIDsStr, transGroupsStr, status, workerID, transIDsStr, transStr sql.NullString
	err := s.db.QueryRowContext(ctx, "SELECT id, name, vhost, source_id, sink_ids, transformation_groups, active, status, worker_id, transformation_ids, transformations FROM connections WHERE id = ?", id).
		Scan(&conn.ID, &conn.Name, &conn.VHost, &conn.SourceID, &sinkIDsStr, &transGroupsStr, &conn.Active, &status, &workerID, &transIDsStr, &transStr)
	if err == sql.ErrNoRows {
		return storage.Connection{}, storage.ErrNotFound
	}
	if err != nil {
		return storage.Connection{}, err
	}
	if status.Valid {
		conn.Status = status.String
	}
	if workerID.Valid {
		conn.WorkerID = workerID.String
	}
	if sinkIDsStr.Valid {
		if err := json.Unmarshal([]byte(sinkIDsStr.String), &conn.SinkIDs); err != nil {
			return storage.Connection{}, err
		}
	}
	if transGroupsStr.Valid && transGroupsStr.String != "" {
		json.Unmarshal([]byte(transGroupsStr.String), &conn.TransformationGroups)
	}
	if transIDsStr.Valid && transIDsStr.String != "" {
		json.Unmarshal([]byte(transIDsStr.String), &conn.TransformationIDs)
	}
	if transStr.Valid && transStr.String != "" {
		json.Unmarshal([]byte(transStr.String), &conn.Transformations)
	}
	return conn, nil
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
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
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

	rows, err := s.db.QueryContext(ctx, baseQuery, args...)
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
	err := s.db.QueryRowContext(ctx, "SELECT id, username, password, full_name, email, role, vhosts FROM users WHERE username = ?", username).
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
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
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

	rows, err := s.db.QueryContext(ctx, baseQuery, args...)
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
	if err == sql.ErrNoRows {
		return storage.VHost{}, storage.ErrNotFound
	}
	if err != nil {
		return storage.VHost{}, err
	}
	return vhost, nil
}

func (s *sqlStorage) ListTransformations(ctx context.Context, filter storage.CommonFilter) ([]storage.Transformation, int, error) {
	baseQuery := "SELECT id, name, type, steps, config, on_failure, execute_if FROM transformations"
	countQuery := "SELECT COUNT(*) FROM transformations"
	var args []interface{}
	var where []string

	if filter.Search != "" {
		search := "%" + filter.Search + "%"
		where = append(where, "(id LIKE ? OR name LIKE ? OR type LIKE ?)")
		args = append(args, search, search, search)
	}

	if len(where) > 0 {
		baseQuery += " WHERE " + strings.Join(where, " AND ")
		countQuery += " WHERE " + strings.Join(where, " AND ")
	}

	var total int
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
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

	rows, err := s.db.QueryContext(ctx, baseQuery, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var transformations []storage.Transformation
	for rows.Next() {
		var trans storage.Transformation
		var stepsStr, configStr, onFailureStr, executeIfStr sql.NullString
		if err := rows.Scan(&trans.ID, &trans.Name, &trans.Type, &stepsStr, &configStr, &onFailureStr, &executeIfStr); err != nil {
			return nil, 0, err
		}

		if stepsStr.Valid {
			_ = json.Unmarshal([]byte(stepsStr.String), &trans.Steps)
		}
		if configStr.Valid {
			_ = json.Unmarshal([]byte(configStr.String), &trans.Config)
		}
		if onFailureStr.Valid {
			trans.OnFailure = onFailureStr.String
		}
		if executeIfStr.Valid {
			trans.ExecuteIf = executeIfStr.String
		}
		transformations = append(transformations, trans)
	}
	return transformations, total, nil
}

func (s *sqlStorage) CreateTransformation(ctx context.Context, trans storage.Transformation) error {
	stepsJSON, _ := json.Marshal(trans.Steps)
	configJSON, _ := json.Marshal(trans.Config)
	_, err := s.db.ExecContext(ctx, "INSERT INTO transformations (id, name, type, steps, config, on_failure, execute_if) VALUES (?, ?, ?, ?, ?, ?, ?)",
		trans.ID, trans.Name, trans.Type, string(stepsJSON), string(configJSON), trans.OnFailure, trans.ExecuteIf)
	return err
}

func (s *sqlStorage) UpdateTransformation(ctx context.Context, trans storage.Transformation) error {
	stepsJSON, _ := json.Marshal(trans.Steps)
	configJSON, _ := json.Marshal(trans.Config)
	_, err := s.db.ExecContext(ctx, "UPDATE transformations SET name = ?, type = ?, steps = ?, config = ?, on_failure = ?, execute_if = ? WHERE id = ?",
		trans.Name, trans.Type, string(stepsJSON), string(configJSON), trans.OnFailure, trans.ExecuteIf, trans.ID)
	return err
}

func (s *sqlStorage) DeleteTransformation(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM transformations WHERE id = ?", id)
	return err
}

func (s *sqlStorage) GetTransformation(ctx context.Context, id string) (storage.Transformation, error) {
	var trans storage.Transformation
	var stepsStr, configStr, onFailureStr, executeIfStr sql.NullString
	err := s.db.QueryRowContext(ctx, "SELECT id, name, type, steps, config, on_failure, execute_if FROM transformations WHERE id = ?", id).
		Scan(&trans.ID, &trans.Name, &trans.Type, &stepsStr, &configStr, &onFailureStr, &executeIfStr)
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
	if onFailureStr.Valid {
		trans.OnFailure = onFailureStr.String
	}
	if executeIfStr.Valid {
		trans.ExecuteIf = executeIfStr.String
	}
	return trans, nil
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
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
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

	rows, err := s.db.QueryContext(ctx, baseQuery, args...)
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
	_, err := s.db.ExecContext(ctx, "INSERT INTO workers (id, name, host, port, description, token, last_seen) VALUES (?, ?, ?, ?, ?, ?, ?)",
		worker.ID, worker.Name, worker.Host, worker.Port, worker.Description, worker.Token, worker.LastSeen)
	return err
}

func (s *sqlStorage) UpdateWorker(ctx context.Context, worker storage.Worker) error {
	_, err := s.db.ExecContext(ctx, "UPDATE workers SET name = ?, host = ?, port = ?, description = ?, token = ?, last_seen = ? WHERE id = ?",
		worker.Name, worker.Host, worker.Port, worker.Description, worker.Token, worker.LastSeen, worker.ID)
	return err
}

func (s *sqlStorage) UpdateWorkerHeartbeat(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE workers SET last_seen = ? WHERE id = ?", time.Now(), id)
	return err
}

func (s *sqlStorage) DeleteWorker(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM workers WHERE id = ?", id)
	return err
}

func (s *sqlStorage) GetWorker(ctx context.Context, id string) (storage.Worker, error) {
	var w storage.Worker
	var token sql.NullString
	var lastSeen sql.NullTime
	err := s.db.QueryRowContext(ctx, "SELECT id, name, host, port, description, token, last_seen FROM workers WHERE id = ?", id).
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
	if filter.Search != "" {
		search := "%" + filter.Search + "%"
		query += " AND (message LIKE ? OR data LIKE ? OR source_id LIKE ? OR sink_id LIKE ? OR connection_id LIKE ?)"
		args = append(args, search, search, search, search, search)
	}

	var total int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*)"+query, args...).Scan(&total); err != nil {
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

	rows, err := s.db.QueryContext(ctx, "SELECT id, timestamp, level, message, action, source_id, sink_id, connection_id, data"+query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var logs []storage.Log
	for rows.Next() {
		var l storage.Log
		var action, sourceID, sinkID, connectionID, data sql.NullString
		if err := rows.Scan(&l.ID, &l.Timestamp, &l.Level, &l.Message, &action, &sourceID, &sinkID, &connectionID, &data); err != nil {
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
		if connectionID.Valid {
			l.ConnectionID = connectionID.String
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

func (s *sqlStorage) GetSetting(ctx context.Context, key string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}

func (s *sqlStorage) SaveSetting(ctx context.Context, key string, value string) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value", key, value)
	return err
}
