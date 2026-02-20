package mariadb

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
	"github.com/user/hermod/pkg/sqlutil"
)

// MariaDBSource implements the hermod.Source interface for MariaDB.
// It uses polling as a baseline and can be extended for binlog CDC.
type MariaDBSource struct {
	connString   string
	useCDC       bool
	tables       []string
	idField      string
	pollInterval time.Duration
	db           *sql.DB
	mu           sync.Mutex
	logger       hermod.Logger
	lastIDs      map[string]any
	msgChan      chan hermod.Message
}

func NewMariaDBSource(connString string, tables []string, idField string, pollInterval time.Duration, useCDC bool) *MariaDBSource {
	if pollInterval <= 0 {
		pollInterval = 5 * time.Second
	}
	return &MariaDBSource{
		connString:   connString,
		tables:       tables,
		idField:      idField,
		pollInterval: pollInterval,
		useCDC:       useCDC,
		lastIDs:      make(map[string]any),
		msgChan:      make(chan hermod.Message, 1000),
	}
}

func (m *MariaDBSource) SetLogger(logger hermod.Logger) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logger = logger
}

func (m *MariaDBSource) log(level, msg string, keysAndValues ...any) {
	m.mu.Lock()
	logger := m.logger
	m.mu.Unlock()

	if logger == nil {
		if len(keysAndValues) > 0 {
			log.Printf("[%s] %s %v", level, msg, keysAndValues)
		} else {
			log.Printf("[%s] %s", level, msg)
		}
		return
	}

	switch level {
	case "DEBUG":
		logger.Debug(msg, keysAndValues...)
	case "INFO":
		logger.Info(msg, keysAndValues...)
	case "WARN":
		logger.Warn(msg, keysAndValues...)
	case "ERROR":
		logger.Error(msg, keysAndValues...)
	}
}

func (m *MariaDBSource) init(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.db != nil {
		return nil
	}

	db, err := sql.Open("mysql", m.connString)
	if err != nil {
		return fmt.Errorf("failed to connect to mariadb: %w", err)
	}
	m.db = db
	return m.db.PingContext(ctx)
}

func (m *MariaDBSource) Read(ctx context.Context) (hermod.Message, error) {
	if err := m.init(ctx); err != nil {
		return nil, err
	}

	if !m.useCDC {
		select {
		case msg := <-m.msgChan:
			return msg, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	// Polling-based CDC for MariaDB
	for {
		select {
		case msg := <-m.msgChan:
			return msg, nil
		default:
		}

		for _, table := range m.tables {
			m.mu.Lock()
			lastID := m.lastIDs[table]
			m.mu.Unlock()

			quotedTable, err := sqlutil.QuoteIdent("mysql", table)
			if err != nil {
				return nil, err
			}

			var query string
			var args []any

			if lastID != nil && m.idField != "" {
				quotedID, _ := sqlutil.QuoteIdent("mysql", m.idField)
				query = fmt.Sprintf("SELECT * FROM %s WHERE %s > ? ORDER BY %s ASC LIMIT 1", quotedTable, quotedID, quotedID)
				args = append(args, lastID)
			} else {
				query = fmt.Sprintf("SELECT * FROM %s LIMIT 1", quotedTable)
			}

			rows, err := m.db.QueryContext(ctx, query, args...)
			if err != nil {
				return nil, fmt.Errorf("mariadb poll error: %w", err)
			}

			if rows.Next() {
				cols, _ := rows.Columns()
				values := make([]any, len(cols))
				ptr := make([]any, len(cols))
				for i := range values {
					ptr[i] = &values[i]
				}

				if err := rows.Scan(ptr...); err != nil {
					rows.Close()
					return nil, err
				}
				rows.Close()

				record := make(map[string]any)
				var currentID any
				for i, col := range cols {
					val := values[i]
					if b, ok := val.([]byte); ok {
						val = string(b)
					}
					record[col] = val
					if col == m.idField {
						currentID = val
					}
				}

				if currentID != nil {
					m.mu.Lock()
					m.lastIDs[table] = currentID
					m.mu.Unlock()
				}

				afterJSON, _ := json.Marshal(message.SanitizeMap(record))
				msg := message.AcquireMessage()
				msg.SetID(fmt.Sprintf("mariadb-%s-%v", table, currentID))
				msg.SetOperation(hermod.OpCreate)
				msg.SetTable(table)
				msg.SetAfter(afterJSON)
				msg.SetMetadata("source", "mariadb")

				return msg, nil
			}
			rows.Close()
		}

		select {
		case msg := <-m.msgChan:
			return msg, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(m.pollInterval):
			// Continue loop
		}
	}
}

func (m *MariaDBSource) Snapshot(ctx context.Context, tables ...string) error {
	if err := m.init(ctx); err != nil {
		return err
	}

	targetTables := tables
	if len(targetTables) == 0 {
		targetTables = m.tables
	}

	if len(targetTables) == 0 {
		var err error
		targetTables, err = m.DiscoverTables(ctx)
		if err != nil {
			return err
		}
	}

	for _, table := range targetTables {
		if err := m.snapshotTable(ctx, table); err != nil {
			return err
		}
	}
	return nil
}

func (m *MariaDBSource) snapshotTable(ctx context.Context, table string) error {
	quoted, err := sqlutil.QuoteIdent("mysql", table)
	if err != nil {
		return fmt.Errorf("invalid table name %q: %w", table, err)
	}

	rows, err := m.db.QueryContext(ctx, fmt.Sprintf("SELECT * FROM %s", quoted))
	if err != nil {
		return fmt.Errorf("failed to query table %q: %w", table, err)
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return err
	}

	for rows.Next() {
		values := make([]any, len(columns))
		valuePtrs := make([]any, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			return err
		}

		record := make(map[string]any)
		for i, colName := range columns {
			val := values[i]
			if b, ok := val.([]byte); ok {
				record[colName] = string(b)
			} else {
				record[colName] = val
			}
		}

		afterJSON, _ := json.Marshal(message.SanitizeMap(record))

		msg := message.AcquireMessage()
		msg.SetID(fmt.Sprintf("snapshot-%s-%d", table, time.Now().UnixNano()))
		msg.SetOperation(hermod.OpSnapshot)
		msg.SetTable(table)
		msg.SetAfter(afterJSON)
		msg.SetMetadata("source", "mariadb")
		msg.SetMetadata("snapshot", "true")

		select {
		case m.msgChan <- msg:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return rows.Err()
}

func (m *MariaDBSource) GetState() map[string]string {
	m.mu.Lock()
	defer m.mu.Unlock()

	state := make(map[string]string)
	for table, id := range m.lastIDs {
		state["last_id:"+table] = fmt.Sprintf("%v", id)
	}
	return state
}

func (m *MariaDBSource) SetState(state map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for k, v := range state {
		if strings.HasPrefix(k, "last_id:") {
			table := strings.TrimPrefix(k, "last_id:")
			m.lastIDs[table] = v
		}
	}
}

func (m *MariaDBSource) Ack(ctx context.Context, msg hermod.Message) error {
	return nil
}

func (m *MariaDBSource) Ping(ctx context.Context) error {
	if err := m.init(ctx); err != nil {
		return err
	}
	return m.db.PingContext(ctx)
}

func (m *MariaDBSource) Close() error {
	m.log("INFO", "Closing MariaDBSource")
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.db != nil {
		err := m.db.Close()
		m.db = nil
		return err
	}
	return nil
}

func (m *MariaDBSource) DiscoverDatabases(ctx context.Context) ([]string, error) {
	if err := m.init(ctx); err != nil {
		return nil, err
	}

	rows, err := m.db.QueryContext(ctx, "SHOW DATABASES")
	if err != nil {
		return nil, fmt.Errorf("failed to query databases: %w", err)
	}
	defer rows.Close()

	var databases []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		databases = append(databases, name)
	}
	return databases, nil
}

func (m *MariaDBSource) DiscoverTables(ctx context.Context) ([]string, error) {
	if err := m.init(ctx); err != nil {
		return nil, err
	}

	rows, err := m.db.QueryContext(ctx, "SHOW TABLES")
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

func (m *MariaDBSource) Sample(ctx context.Context, table string) (hermod.Message, error) {
	if err := m.init(ctx); err != nil {
		return nil, err
	}

	quoted, err := sqlutil.QuoteIdent("mysql", table)
	if err != nil {
		return nil, fmt.Errorf("invalid table name: %w", err)
	}
	rows, err := m.db.QueryContext(ctx, fmt.Sprintf("SELECT * FROM %s LIMIT 1", quoted))
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

	columns := make([]any, len(cols))
	columnPointers := make([]any, len(cols))
	for i := range columns {
		columnPointers[i] = &columns[i]
	}

	if err := rows.Scan(columnPointers...); err != nil {
		return nil, err
	}

	record := make(map[string]any)
	for i, colName := range cols {
		val := columns[i]
		if b, ok := val.([]byte); ok {
			record[colName] = string(b)
		} else {
			record[colName] = val
		}
	}

	afterJSON, _ := json.Marshal(message.SanitizeMap(record))

	msg := message.AcquireMessage()
	msg.SetID(fmt.Sprintf("sample-%s-%d", table, time.Now().Unix()))
	msg.SetOperation(hermod.OpSnapshot)
	msg.SetTable(table)
	msg.SetAfter(afterJSON)
	msg.SetMetadata("source", "mariadb")
	msg.SetMetadata("sample", "true")

	return msg, nil
}
