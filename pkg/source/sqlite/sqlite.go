package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
	"github.com/user/hermod/pkg/sqlutil"
	_ "modernc.org/sqlite"
)

// SQLiteSource implements the hermod.Source interface for SQLite.
// Since SQLite doesn't have native CDC like Postgres, this implementation
// might rely on triggers or polling. For now, it's a placeholder consistent with other sources.
type SQLiteSource struct {
	dbPath  string
	tables  []string
	useCDC  bool
	db      *sql.DB
	lastIDs map[string]int64
	msgChan chan hermod.Message
}

func NewSQLiteSource(dbPath string, tables []string, useCDC bool) *SQLiteSource {
	return &SQLiteSource{
		dbPath:  dbPath,
		tables:  tables,
		useCDC:  useCDC,
		lastIDs: make(map[string]int64),
		msgChan: make(chan hermod.Message, 1000),
	}
}

func (s *SQLiteSource) Read(ctx context.Context) (hermod.Message, error) {
	if s.db == nil {
		if err := s.init(ctx); err != nil {
			return nil, err
		}
	}

	if !s.useCDC {
		// If not CDC, we only return messages from msgChan (e.g. snapshots)
		select {
		case msg := <-s.msgChan:
			return msg, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	// Simple polling-based CDC for SQLite using rowid
	for {
		select {
		case msg := <-s.msgChan:
			return msg, nil
		default:
		}

		for _, table := range s.tables {
			lastID := s.lastIDs[table]
			query := fmt.Sprintf("SELECT rowid AS _hermod_rowid, * FROM %s WHERE rowid > %d ORDER BY rowid ASC LIMIT 1", table, lastID)

			rows, err := s.db.QueryContext(ctx, query)
			if err != nil {
				return nil, err
			}

			if rows.Next() {
				cols, _ := rows.Columns()
				values := make([]interface{}, len(cols))
				pointers := make([]interface{}, len(cols))
				for i := range values {
					pointers[i] = &values[i]
				}

				if err := rows.Scan(pointers...); err != nil {
					rows.Close()
					return nil, err
				}
				rows.Close()

				var currentRowID int64
				data := make(map[string]interface{})
				for i, col := range cols {
					val := values[i]
					if b, ok := val.([]byte); ok {
						val = string(b)
					}
					if col == "_hermod_rowid" {
						currentRowID = val.(int64)
					} else {
						data[col] = val
					}
				}

				s.lastIDs[table] = currentRowID

				msg := message.AcquireMessage()
				msg.SetID(fmt.Sprintf("sqlite-%s-%d", table, currentRowID))
				msg.SetOperation(hermod.OpCreate)
				msg.SetTable(table)
				for k, v := range data {
					msg.SetData(k, v)
				}
				msg.SetMetadata("source", "sqlite")
				return msg, nil
			}
			rows.Close()
		}

		select {
		case msg := <-s.msgChan:
			return msg, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(1 * time.Second):
			// Poll again
		}
	}
}

func (s *SQLiteSource) init(ctx context.Context) error {
	dsn := s.dbPath
	if !strings.Contains(dsn, "?") {
		dsn += "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return fmt.Errorf("failed to open sqlite database: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return fmt.Errorf("failed to ping sqlite database: %w", err)
	}
	s.db = db
	return nil
}

func (s *SQLiteSource) Ack(ctx context.Context, msg hermod.Message) error {
	// Acknowledgement logic for SQLite if needed (e.g. updating a watermark table)
	return nil
}

func (s *SQLiteSource) IsReady(ctx context.Context) error {
	if err := s.Ping(ctx); err != nil {
		return fmt.Errorf("sqlite connection failed: %w", err)
	}

	// For SQLite, "readiness" means the file exists and is accessible.
	// s.init already does a Ping, which for SQLite typically ensures the file is openable.
	// We can add a check to see if tables exist if they are configured.
	if len(s.tables) > 0 {
		for _, table := range s.tables {
			var name string
			err := s.db.QueryRowContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name)
			if err != nil {
				if err == sql.ErrNoRows {
					return fmt.Errorf("sqlite table '%s' does not exist in database '%s'", table, s.dbPath)
				}
				return fmt.Errorf("failed to verify sqlite table '%s': %w", table, err)
			}
		}
	}

	return nil
}

func (s *SQLiteSource) Ping(ctx context.Context) error {
	if s.db == nil {
		if err := s.init(ctx); err != nil {
			return err
		}
	}
	return s.db.PingContext(ctx)
}

func (s *SQLiteSource) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

func (s *SQLiteSource) GetState() map[string]string {
	state := make(map[string]string)
	for table, id := range s.lastIDs {
		state[table] = fmt.Sprintf("%d", id)
	}
	return state
}

func (s *SQLiteSource) SetState(state map[string]string) {
	if s.lastIDs == nil {
		s.lastIDs = make(map[string]int64)
	}
	for table, idStr := range state {
		var id int64
		fmt.Sscanf(idStr, "%d", &id)
		s.lastIDs[table] = id
	}
}

func (s *SQLiteSource) DiscoverDatabases(ctx context.Context) ([]string, error) {
	return []string{"main"}, nil
}

func (s *SQLiteSource) DiscoverTables(ctx context.Context) ([]string, error) {
	if s.db == nil {
		if err := s.init(ctx); err != nil {
			return nil, err
		}
	}

	rows, err := s.db.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'")
	if err != nil {
		return nil, fmt.Errorf("failed to query tables: %w", err)
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		tables = append(tables, name)
	}
	return tables, nil
}

func (s *SQLiteSource) Sample(ctx context.Context, table string) (hermod.Message, error) {
	if s.db == nil {
		if err := s.init(ctx); err != nil {
			return nil, err
		}
	}

	quoted, err := sqlutil.QuoteIdent("sqlite", table)
	if err != nil {
		return nil, fmt.Errorf("invalid table name: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf("SELECT * FROM %s LIMIT 1", quoted))
	if err != nil {
		return nil, fmt.Errorf("failed to query sample record: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		return nil, fmt.Errorf("no records found in table %s", table)
	}

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	columns := make([]interface{}, len(cols))
	columnPointers := make([]interface{}, len(cols))
	for i := range columns {
		columnPointers[i] = &columns[i]
	}

	if err := rows.Scan(columnPointers...); err != nil {
		return nil, err
	}

	record := make(map[string]interface{})
	for i, colName := range cols {
		val := columns[i]
		if b, ok := val.([]byte); ok {
			record[colName] = string(b)
		} else {
			record[colName] = val
		}
	}

	msg := message.AcquireMessage()
	msg.SetID(fmt.Sprintf("sample-%s-%d", table, time.Now().Unix()))
	msg.SetOperation(hermod.OpSnapshot)
	msg.SetTable(table)
	for k, v := range message.SanitizeMap(record) {
		msg.SetData(k, v)
	}
	msg.SetMetadata("source", "sqlite")
	msg.SetMetadata("sample", "true")

	return msg, nil
}

func (s *SQLiteSource) Snapshot(ctx context.Context, tables ...string) error {
	if s.db == nil {
		if err := s.init(ctx); err != nil {
			return err
		}
	}

	targetTables := tables
	if len(targetTables) == 0 {
		targetTables = s.tables
	}

	if len(targetTables) == 0 {
		var err error
		targetTables, err = s.DiscoverTables(ctx)
		if err != nil {
			return err
		}
	}

	for _, table := range targetTables {
		if err := s.snapshotTable(ctx, table); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteSource) snapshotTable(ctx context.Context, table string) error {
	quoted, err := sqlutil.QuoteIdent("sqlite", table)
	if err != nil {
		return fmt.Errorf("invalid table name %q: %w", table, err)
	}

	rows, err := s.db.QueryContext(ctx, fmt.Sprintf("SELECT * FROM %s", quoted))
	if err != nil {
		return fmt.Errorf("failed to query table %q: %w", table, err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return err
	}

	for rows.Next() {
		columns := make([]interface{}, len(cols))
		columnPointers := make([]interface{}, len(cols))
		for i := range columns {
			columnPointers[i] = &columns[i]
		}

		if err := rows.Scan(columnPointers...); err != nil {
			return err
		}

		record := make(map[string]interface{})
		for i, colName := range cols {
			val := columns[i]
			if b, ok := val.([]byte); ok {
				record[colName] = string(b)
			} else {
				record[colName] = val
			}
		}

		msg := message.AcquireMessage()
		msg.SetID(fmt.Sprintf("snapshot-%s-%d", table, time.Now().UnixNano()))
		msg.SetOperation(hermod.OpSnapshot)
		msg.SetTable(table)
		for k, v := range message.SanitizeMap(record) {
			msg.SetData(k, v)
		}
		msg.SetMetadata("source", "sqlite")
		msg.SetMetadata("snapshot", "true")

		select {
		case s.msgChan <- msg:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return rows.Err()
}
