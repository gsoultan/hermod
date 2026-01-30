package clickhouse

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
	"github.com/user/hermod/pkg/sqlutil"
)

// ClickHouseSource implements the hermod.Source interface for ClickHouse.
type ClickHouseSource struct {
	connString string
	useCDC     bool
	conn       clickhouse.Conn
	mu         sync.Mutex
	logger     hermod.Logger
}

func NewClickHouseSource(connString string, useCDC bool) *ClickHouseSource {
	return &ClickHouseSource{
		connString: connString,
		useCDC:     useCDC,
	}
}

func (c *ClickHouseSource) SetLogger(logger hermod.Logger) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.logger = logger
}

func (c *ClickHouseSource) log(level, msg string, keysAndValues ...interface{}) {
	c.mu.Lock()
	logger := c.logger
	c.mu.Unlock()

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

func (c *ClickHouseSource) init(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		return nil
	}

	options, err := clickhouse.ParseDSN(c.connString)
	if err != nil {
		return fmt.Errorf("failed to parse clickhouse DSN: %w", err)
	}

	conn, err := clickhouse.Open(options)
	if err != nil {
		return fmt.Errorf("failed to connect to clickhouse: %w", err)
	}
	c.conn = conn
	return c.conn.Ping(ctx)
}

func (c *ClickHouseSource) Read(ctx context.Context) (hermod.Message, error) {
	if err := c.init(ctx); err != nil {
		return nil, err
	}

	// For baseline, ClickHouse Read blocks.
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(5 * time.Second):
		return nil, nil
	}
}

func (c *ClickHouseSource) Ack(ctx context.Context, msg hermod.Message) error {
	return nil
}

func (c *ClickHouseSource) Ping(ctx context.Context) error {
	if err := c.init(ctx); err != nil {
		return err
	}
	return c.conn.Ping(ctx)
}

func (c *ClickHouseSource) Close() error {
	c.log("INFO", "Closing ClickHouseSource")
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		err := c.conn.Close()
		c.conn = nil
		return err
	}
	return nil
}

func (c *ClickHouseSource) DiscoverDatabases(ctx context.Context) ([]string, error) {
	if err := c.init(ctx); err != nil {
		return nil, err
	}

	rows, err := c.conn.Query(ctx, "SHOW DATABASES")
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

func (c *ClickHouseSource) DiscoverTables(ctx context.Context) ([]string, error) {
	if err := c.init(ctx); err != nil {
		return nil, err
	}

	rows, err := c.conn.Query(ctx, "SHOW TABLES")
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

func (c *ClickHouseSource) Sample(ctx context.Context, table string) (hermod.Message, error) {
	if err := c.init(ctx); err != nil {
		return nil, err
	}

	quoted, err := sqlutil.QuoteIdent("clickhouse", table)
	if err != nil {
		return nil, fmt.Errorf("invalid table name: %w", err)
	}
	rows, err := c.conn.Query(ctx, fmt.Sprintf("SELECT * FROM %s LIMIT 1", quoted))
	if err != nil {
		return nil, fmt.Errorf("failed to query sample record: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		return nil, fmt.Errorf("no records found in table %s", table)
	}

	columns := rows.Columns()
	values := make([]interface{}, len(columns))
	// clickhouse-go v2 uses Scan for results
	dest := make([]interface{}, len(columns))
	for i := range dest {
		dest[i] = &values[i]
	}

	if err := rows.Scan(dest...); err != nil {
		return nil, err
	}

	record := make(map[string]interface{})
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
	msg.SetID(fmt.Sprintf("sample-%s-%d", table, time.Now().Unix()))
	msg.SetOperation(hermod.OpSnapshot)
	msg.SetTable(table)
	msg.SetAfter(afterJSON)
	msg.SetMetadata("source", "clickhouse")
	msg.SetMetadata("sample", "true")

	return msg, nil
}
