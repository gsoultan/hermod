package clickhouse

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
	"github.com/user/hermod/pkg/sqlutil"
)

// ClickHouseSource implements the hermod.Source interface for ClickHouse.
type ClickHouseSource struct {
	connString   string
	useCDC       bool
	tables       []string
	idField      string
	pollInterval time.Duration
	conn         clickhouse.Conn
	mu           sync.Mutex
	logger       hermod.Logger
	lastIDs      map[string]interface{}
	msgChan      chan hermod.Message
}

func NewClickHouseSource(connString string, tables []string, idField string, pollInterval time.Duration, useCDC bool) *ClickHouseSource {
	if pollInterval <= 0 {
		pollInterval = 5 * time.Second
	}
	return &ClickHouseSource{
		connString:   connString,
		tables:       tables,
		idField:      idField,
		pollInterval: pollInterval,
		useCDC:       useCDC,
		lastIDs:      make(map[string]interface{}),
		msgChan:      make(chan hermod.Message, 1000),
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

	if !c.useCDC {
		select {
		case msg := <-c.msgChan:
			return msg, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	for {
		select {
		case msg := <-c.msgChan:
			return msg, nil
		default:
		}

		for _, table := range c.tables {
			c.mu.Lock()
			lastID := c.lastIDs[table]
			c.mu.Unlock()

			quotedTable, err := sqlutil.QuoteIdent("clickhouse", table)
			if err != nil {
				return nil, err
			}

			var query string
			var args []interface{}

			if lastID != nil && c.idField != "" {
				quotedID, _ := sqlutil.QuoteIdent("clickhouse", c.idField)
				query = fmt.Sprintf("SELECT * FROM %s WHERE %s > ? ORDER BY %s ASC LIMIT 1", quotedTable, quotedID, quotedID)
				args = append(args, lastID)
			} else {
				query = fmt.Sprintf("SELECT * FROM %s LIMIT 1", quotedTable)
			}

			rows, err := c.conn.Query(ctx, query, args...)
			if err != nil {
				return nil, fmt.Errorf("clickhouse poll error: %w", err)
			}

			if rows.Next() {
				columns := rows.Columns()
				values := make([]interface{}, len(columns))
				dest := make([]interface{}, len(columns))
				for i := range dest {
					dest[i] = &values[i]
				}

				if err := rows.Scan(dest...); err != nil {
					rows.Close()
					return nil, err
				}
				rows.Close()

				record := make(map[string]interface{})
				var currentID interface{}
				for i, col := range columns {
					val := values[i]
					if b, ok := val.([]byte); ok {
						val = string(b)
					}
					record[col] = val
					if col == c.idField {
						currentID = val
					}
				}

				if currentID != nil {
					c.mu.Lock()
					c.lastIDs[table] = currentID
					c.mu.Unlock()
				}

				afterJSON, _ := json.Marshal(message.SanitizeMap(record))
				msg := message.AcquireMessage()
				msg.SetID(fmt.Sprintf("clickhouse-%s-%v", table, currentID))
				msg.SetOperation(hermod.OpCreate)
				msg.SetTable(table)
				msg.SetAfter(afterJSON)
				msg.SetMetadata("source", "clickhouse")

				return msg, nil
			}
			rows.Close()
		}

		select {
		case msg := <-c.msgChan:
			return msg, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(c.pollInterval):
		}
	}
}

func (c *ClickHouseSource) Snapshot(ctx context.Context, tables ...string) error {
	if err := c.init(ctx); err != nil {
		return err
	}

	targetTables := tables
	if len(targetTables) == 0 {
		targetTables = c.tables
	}

	if len(targetTables) == 0 {
		var err error
		targetTables, err = c.DiscoverTables(ctx)
		if err != nil {
			return err
		}
	}

	for _, table := range targetTables {
		if err := c.snapshotTable(ctx, table); err != nil {
			return err
		}
	}
	return nil
}

func (c *ClickHouseSource) snapshotTable(ctx context.Context, table string) error {
	quoted, err := sqlutil.QuoteIdent("clickhouse", table)
	if err != nil {
		return fmt.Errorf("invalid table name %q: %w", table, err)
	}

	rows, err := c.conn.Query(ctx, fmt.Sprintf("SELECT * FROM %s", quoted))
	if err != nil {
		return fmt.Errorf("failed to query table %q: %w", table, err)
	}
	defer rows.Close()

	columns := rows.Columns()
	for rows.Next() {
		values := make([]interface{}, len(columns))
		dest := make([]interface{}, len(columns))
		for i := range dest {
			dest[i] = &values[i]
		}

		if err := rows.Scan(dest...); err != nil {
			return err
		}

		record := make(map[string]interface{})
		for i, colName := range columns {
			val := values[i]
			if b, ok := val.([]byte); ok {
				val = string(b)
			}
			record[colName] = val
		}

		afterJSON, _ := json.Marshal(message.SanitizeMap(record))

		msg := message.AcquireMessage()
		msg.SetID(fmt.Sprintf("snapshot-%s-%d", table, time.Now().UnixNano()))
		msg.SetOperation(hermod.OpSnapshot)
		msg.SetTable(table)
		msg.SetAfter(afterJSON)
		msg.SetMetadata("source", "clickhouse")
		msg.SetMetadata("snapshot", "true")

		select {
		case c.msgChan <- msg:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return rows.Err()
}

func (c *ClickHouseSource) GetState() map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()

	state := make(map[string]string)
	for table, id := range c.lastIDs {
		state["last_id:"+table] = fmt.Sprintf("%v", id)
	}
	return state
}

func (c *ClickHouseSource) SetState(state map[string]string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for k, v := range state {
		if strings.HasPrefix(k, "last_id:") {
			table := strings.TrimPrefix(k, "last_id:")
			c.lastIDs[table] = v
		}
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

func (c *ClickHouseSource) DiscoverColumns(ctx context.Context, table string) ([]hermod.ColumnInfo, error) {
	if err := c.init(ctx); err != nil {
		return nil, err
	}

	query := fmt.Sprintf("SELECT name, type, is_nullable = 'YES', is_in_primary_key, default_expression FROM system.columns WHERE table = '%s'", table)
	rows, err := c.conn.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var columns []hermod.ColumnInfo
	for rows.Next() {
		var col hermod.ColumnInfo
		var def string
		var isNullable, isPK bool
		if err := rows.Scan(&col.Name, &col.Type, &isNullable, &isPK, &def); err != nil {
			return nil, err
		}
		col.IsNullable = isNullable
		col.IsPK = isPK
		col.IsIdentity = strings.Contains(strings.ToLower(def), "generateuuidv4") || strings.Contains(strings.ToLower(def), "nextval")
		col.Default = def
		columns = append(columns, col)
	}
	return columns, nil
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
