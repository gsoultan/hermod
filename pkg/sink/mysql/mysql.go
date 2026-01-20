package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

// MySQLSink implements the hermod.Sink interface for MySQL.
type MySQLSink struct {
	connString string
	db         *sql.DB
}

func NewMySQLSink(connString string) *MySQLSink {
	return &MySQLSink{
		connString: connString,
	}
}

func (s *MySQLSink) Write(ctx context.Context, msg hermod.Message) error {
	if msg == nil {
		return nil
	}
	if s.db == nil {
		if err := s.init(ctx); err != nil {
			return err
		}
	}

	// More production-ready implementation:
	// Handle different operations and don't assume a fixed schema.
	table := msg.Table()
	if msg.Schema() != "" {
		table = fmt.Sprintf("%s.%s", msg.Schema(), table)
	}

	op := msg.Operation()
	if op == "" {
		op = hermod.OpCreate
	}

	switch op {
	case hermod.OpCreate, hermod.OpSnapshot, hermod.OpUpdate:
		return s.upsert(ctx, table, msg)
	case hermod.OpDelete:
		query := fmt.Sprintf("DELETE FROM %s WHERE id = ?", table)
		_, err := s.db.ExecContext(ctx, query, msg.ID())
		return err
	default:
		return fmt.Errorf("unsupported operation: %s", op)
	}
}

func (s *MySQLSink) upsert(ctx context.Context, table string, msg hermod.Message) error {
	// For a truly generic sink, we would need to parse the JSON and build the query.
	// As a compromise for production readiness without a full ORM, we'll assume 'id' exists.
	query := fmt.Sprintf("INSERT INTO %s (id, data) VALUES (?, ?) ON DUPLICATE KEY UPDATE data = VALUES(data)", table)
	_, err := s.db.ExecContext(ctx, query, msg.ID(), msg.Payload())
	if err != nil {
		return fmt.Errorf("failed to write to mysql: %w", err)
	}
	return nil
}

func (s *MySQLSink) init(ctx context.Context) error {
	db, err := sql.Open("mysql", s.connString)
	if err != nil {
		return fmt.Errorf("failed to connect to mysql: %w", err)
	}
	s.db = db
	return s.db.PingContext(ctx)
}

func (s *MySQLSink) Ping(ctx context.Context) error {
	if s.db == nil {
		if err := s.init(ctx); err != nil {
			return err
		}
	}
	return s.db.PingContext(ctx)
}

func (s *MySQLSink) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

func (s *MySQLSink) DiscoverDatabases(ctx context.Context) ([]string, error) {
	if s.db == nil {
		if err := s.init(ctx); err != nil {
			return nil, err
		}
	}

	rows, err := s.db.QueryContext(ctx, "SHOW DATABASES")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var databases []string
	for rows.Next() {
		var db string
		if err := rows.Scan(&db); err != nil {
			return nil, err
		}
		databases = append(databases, db)
	}
	return databases, nil
}

func (s *MySQLSink) DiscoverTables(ctx context.Context) ([]string, error) {
	if s.db == nil {
		if err := s.init(ctx); err != nil {
			return nil, err
		}
	}

	rows, err := s.db.QueryContext(ctx, "SHOW TABLES")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			return nil, err
		}
		tables = append(tables, table)
	}
	return tables, nil
}

func (s *MySQLSink) Sample(ctx context.Context, table string) (hermod.Message, error) {
	if s.db == nil {
		if err := s.init(ctx); err != nil {
			return nil, err
		}
	}

	query := fmt.Sprintf("SELECT * FROM %s LIMIT 1", table)
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	if !rows.Next() {
		return nil, fmt.Errorf("no data found in table %s", table)
	}

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	values := make([]interface{}, len(cols))
	valuePtrs := make([]interface{}, len(cols))
	for i := range values {
		valuePtrs[i] = &values[i]
	}

	if err := rows.Scan(valuePtrs...); err != nil {
		return nil, err
	}

	record := make(map[string]interface{})
	for i, col := range cols {
		val := values[i]
		if b, ok := val.([]byte); ok {
			record[col] = string(b)
		} else {
			record[col] = val
		}
	}

	afterJSON, _ := json.Marshal(message.SanitizeMap(record))

	msg := message.AcquireMessage()
	msg.SetID(fmt.Sprintf("sample-%s-%d", table, time.Now().Unix()))
	msg.SetOperation(hermod.OpSnapshot)
	msg.SetTable(table)
	msg.SetAfter(afterJSON)
	msg.SetMetadata("source", "mysql_sink")
	msg.SetMetadata("sample", "true")

	return msg, nil
}
