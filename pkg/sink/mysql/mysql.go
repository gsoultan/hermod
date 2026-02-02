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
	"github.com/user/hermod/pkg/sqlutil"
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
	return s.WriteBatch(ctx, []hermod.Message{msg})
}

func (s *MySQLSink) WriteBatch(ctx context.Context, msgs []hermod.Message) error {
	if len(msgs) == 0 {
		return nil
	}
	if s.db == nil {
		if err := s.init(ctx); err != nil {
			return err
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin mysql transaction: %w", err)
	}
	defer tx.Rollback()

	for _, msg := range msgs {
		if msg == nil {
			continue
		}

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
			query := fmt.Sprintf(commonQueries[QueryUpsert], table)
			_, err = tx.ExecContext(ctx, query, msg.ID(), msg.Payload())
		case hermod.OpDelete:
			query := fmt.Sprintf(commonQueries[QueryDelete], table)
			_, err = tx.ExecContext(ctx, query, msg.ID())
		default:
			err = fmt.Errorf("unsupported operation: %s", op)
		}

		if err != nil {
			return fmt.Errorf("mysql batch write error on message %s: %w", msg.ID(), err)
		}
	}

	return tx.Commit()
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

	rows, err := s.db.QueryContext(ctx, commonQueries[QueryShowDatabases])
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

	rows, err := s.db.QueryContext(ctx, commonQueries[QueryShowTables])
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
	msgs, err := s.Browse(ctx, table, 1)
	if err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		return nil, fmt.Errorf("no data found in table %s", table)
	}
	return msgs[0], nil
}

func (s *MySQLSink) Browse(ctx context.Context, table string, limit int) ([]hermod.Message, error) {
	if s.db == nil {
		if err := s.init(ctx); err != nil {
			return nil, err
		}
	}

	quoted, err := sqlutil.QuoteIdent("mysql", table)
	if err != nil {
		return nil, fmt.Errorf("invalid table name: %w", err)
	}
	query := fmt.Sprintf(commonQueries[QueryBrowse], quoted, limit)
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []hermod.Message
	for rows.Next() {
		cols, err := rows.Columns()
		if err != nil {
			return nil, fmt.Errorf("failed to get columns: %w", err)
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
		msg.SetID(fmt.Sprintf("sample-%s-%d-%d", table, time.Now().Unix(), len(msgs)))
		msg.SetOperation(hermod.OpSnapshot)
		msg.SetTable(table)
		msg.SetAfter(afterJSON)
		msg.SetMetadata("source", "mysql_sink")
		msg.SetMetadata("sample", "true")
		msgs = append(msgs, msg)
	}

	return msgs, nil
}
