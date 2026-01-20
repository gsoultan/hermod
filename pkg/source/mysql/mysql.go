package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

// MySQLSource implements the hermod.Source interface for MySQL CDC.
type MySQLSource struct {
	connString string
	useCDC     bool
	db         *sql.DB
	mu         sync.Mutex
	logger     hermod.Logger
}

func NewMySQLSource(connString string, useCDC bool) *MySQLSource {
	return &MySQLSource{
		connString: connString,
		useCDC:     useCDC,
	}
}

func (m *MySQLSource) SetLogger(logger hermod.Logger) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logger = logger
}

func (m *MySQLSource) log(level, msg string, keysAndValues ...interface{}) {
	m.mu.Lock()
	logger := m.logger
	m.mu.Unlock()

	if logger == nil {
		// Fallback to standard log if no structured logger is set, to ensure timestamps
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

func (m *MySQLSource) init(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.db != nil {
		return nil
	}

	db, err := sql.Open("mysql", m.connString)
	if err != nil {
		return fmt.Errorf("failed to connect to mysql: %w", err)
	}
	m.db = db
	return m.db.PingContext(ctx)
}

func (m *MySQLSource) Read(ctx context.Context) (hermod.Message, error) {
	if !m.useCDC {
		if m.db == nil {
			if err := m.init(ctx); err != nil {
				return nil, err
			}
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}

	m.mu.Lock()
	db := m.db
	m.mu.Unlock()

	if db == nil {
		if err := m.init(ctx); err != nil {
			return nil, err
		}
	}

	// For production readiness without go-mysql, we'd implement polling.
	// This is a placeholder that respects context and can be extended to real polling.
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(5 * time.Second):
		// In a real polling implementation, we'd query for changes since last LSN/Timestamp.
		return nil, nil // No new data
	}
}

func (m *MySQLSource) Ack(ctx context.Context, msg hermod.Message) error {
	return nil
}

func (m *MySQLSource) Ping(ctx context.Context) error {
	m.mu.Lock()
	db := m.db
	m.mu.Unlock()

	if db == nil {
		// Just connect and ping, don't trigger anything else if m.init was heavier
		// In this case m.init is already light, but we should be consistent
		db, err := sql.Open("mysql", m.connString)
		if err != nil {
			return fmt.Errorf("failed to open mysql connection for ping: %w", err)
		}
		defer db.Close()
		return db.PingContext(ctx)
	}
	return db.PingContext(ctx)
}

func (m *MySQLSource) Close() error {
	m.log("INFO", "Closing MySQLSource")
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.db != nil {
		err := m.db.Close()
		m.db = nil
		return err
	}
	return nil
}

func (m *MySQLSource) DiscoverDatabases(ctx context.Context) ([]string, error) {
	if m.db == nil {
		if err := m.init(ctx); err != nil {
			return nil, err
		}
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

func (m *MySQLSource) DiscoverTables(ctx context.Context) ([]string, error) {
	if m.db == nil {
		if err := m.init(ctx); err != nil {
			return nil, err
		}
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

func (m *MySQLSource) Sample(ctx context.Context, table string) (hermod.Message, error) {
	if m.db == nil {
		if err := m.init(ctx); err != nil {
			return nil, err
		}
	}

	rows, err := m.db.QueryContext(ctx, fmt.Sprintf("SELECT * FROM %s LIMIT 1", table))
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

	afterJSON, _ := json.Marshal(message.SanitizeMap(record))

	msg := message.AcquireMessage()
	msg.SetID(fmt.Sprintf("sample-%s-%d", table, time.Now().Unix()))
	msg.SetOperation(hermod.OpSnapshot)
	msg.SetTable(table)
	msg.SetAfter(afterJSON)
	msg.SetMetadata("source", "mysql")
	msg.SetMetadata("sample", "true")

	return msg, nil
}
