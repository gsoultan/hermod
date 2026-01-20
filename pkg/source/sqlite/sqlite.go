package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
	_ "modernc.org/sqlite"
)

// SQLiteSource implements the hermod.Source interface for SQLite.
// Since SQLite doesn't have native CDC like Postgres, this implementation
// might rely on triggers or polling. For now, it's a placeholder consistent with other sources.
type SQLiteSource struct {
	dbPath string
	tables []string
	useCDC bool
	db     *sql.DB
}

func NewSQLiteSource(dbPath string, tables []string, useCDC bool) *SQLiteSource {
	return &SQLiteSource{
		dbPath: dbPath,
		tables: tables,
		useCDC: useCDC,
	}
}

func (s *SQLiteSource) Read(ctx context.Context) (hermod.Message, error) {
	if !s.useCDC {
		if s.db == nil {
			if err := s.init(ctx); err != nil {
				return nil, err
			}
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}

	if s.db == nil {
		if err := s.init(ctx); err != nil {
			return nil, err
		}
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		// Placeholder logic for SQLite "CDC"
		msg := message.AcquireMessage()
		msg.SetID("sqlite-1")
		msg.SetOperation(hermod.OpCreate)
		if len(s.tables) > 0 {
			msg.SetTable(s.tables[0])
		}
		msg.SetMetadata("source", "sqlite")
		return msg, nil
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

	rows, err := s.db.QueryContext(ctx, fmt.Sprintf("SELECT * FROM %s LIMIT 1", table))
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
	for k, v := range message.SanitizeMap(record) {
		msg.SetData(k, v)
	}
	msg.SetMetadata("source", "sqlite")
	msg.SetMetadata("sample", "true")

	return msg, nil
}
