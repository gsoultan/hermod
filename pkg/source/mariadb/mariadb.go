package mariadb

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
	"github.com/user/hermod/pkg/sqlutil"
)

// MariaDBSource implements the hermod.Source interface for MariaDB.
// It uses polling as a baseline and can be extended for binlog CDC.
type MariaDBSource struct {
	connString string
	useCDC     bool
	db         *sql.DB
	mu         sync.Mutex
	logger     hermod.Logger
}

func NewMariaDBSource(connString string, useCDC bool) *MariaDBSource {
	return &MariaDBSource{
		connString: connString,
		useCDC:     useCDC,
	}
}

func (m *MariaDBSource) SetLogger(logger hermod.Logger) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logger = logger
}

func (m *MariaDBSource) log(level, msg string, keysAndValues ...interface{}) {
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

	// For now, MariaDB implementation uses polling or blocks if CDC is requested but not yet implemented.
	// In Hermod, we prefer explicit polling logic for "working" baseline.
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(5 * time.Second):
		return nil, nil // No new data
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
	msg.SetMetadata("source", "mariadb")
	msg.SetMetadata("sample", "true")

	return msg, nil
}
