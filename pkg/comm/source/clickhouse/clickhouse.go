package clickhouse

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"reflect"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
	sourcebuf "github.com/gsoultan/hermod/pkg/comm/source"
	"github.com/gsoultan/hermod/pkg/infra/sqlutil"
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
	lastIDs      map[string]any
	// ackedIDs is the watermark of the last *acknowledged* row per table, and
	// the only one GetState reports. lastIDs above is how far reading has got,
	// which is further ahead whenever messages are in flight; it keeps the
	// poller moving inside this process and is deliberately not persisted.
	ackedIDs map[string]any
	msgChan  chan hermod.Message
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
		lastIDs:      make(map[string]any),
		ackedIDs:     make(map[string]any),
		msgChan:      make(chan hermod.Message, sourcebuf.DefaultSourceBuffer),
	}
}

func (c *ClickHouseSource) SetLogger(logger hermod.Logger) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.logger = logger
}

func (c *ClickHouseSource) log(level, msg string, keysAndValues ...any) {
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
	if c.conn != nil {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()

	options, err := clickhouse.ParseDSN(c.connString)
	if err != nil {
		return fmt.Errorf("failed to parse clickhouse DSN: %w", err)
	}

	conn, err := clickhouse.Open(options)
	if err != nil {
		return fmt.Errorf("failed to connect to clickhouse: %w", err)
	}

	if err := conn.Ping(ctx); err != nil {
		conn.Close()
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		conn.Close()
		return nil
	}
	c.conn = conn
	return nil
}

// scanRow reads one row into a map, allocating each destination from the type
// the driver says the column has.
//
// Every scan here used to point at an interface{}, which the ClickHouse driver
// refuses outright — "converting UInt64 to *interface {} is unsupported. try
// using *uint64". A UInt64 is the most ordinary column type there is, so the
// source could not read a numeric column at all: the poll failed on the first
// row of any table with one, and had done since it was written. Nothing caught
// it because nothing had ever run this against a server.
//
// ColumnTypes gives the concrete Go type per column, so the destinations are
// allocated to match and the driver has something it can fill.
func scanRow(rows driver.Rows) (map[string]any, error) {
	columns := rows.Columns()
	types := rows.ColumnTypes()

	dest := make([]any, len(columns))
	for i := range dest {
		if i < len(types) && types[i].ScanType() != nil {
			dest[i] = reflect.New(types[i].ScanType()).Interface()
		} else {
			var v any
			dest[i] = &v
		}
	}
	if err := rows.Scan(dest...); err != nil {
		return nil, err
	}

	record := make(map[string]any, len(columns))
	for i, col := range columns {
		val := reflect.ValueOf(dest[i]).Elem().Interface()
		if b, ok := val.([]byte); ok {
			val = string(b)
		}
		record[col] = val
	}
	return record, nil
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

			var query string
			var args []any
			var err error

			if lastID != nil && c.idField != "" {
				query, err = sqlutil.BuildIncrementalQuery("clickhouse", table, c.idField)
				if err != nil {
					return nil, err
				}
				args = append(args, lastID)
			} else {
				query, err = sqlutil.BuildFirstRowQuery("clickhouse", table)
				if err != nil {
					return nil, err
				}
			}

			rows, err := c.conn.Query(ctx, query, args...)
			if err != nil {
				return nil, fmt.Errorf("clickhouse poll error: %w", err)
			}

			if rows.Next() {
				record, err := scanRow(rows)
				if err != nil {
					rows.Close()
					return nil, err
				}
				rows.Close()

				currentID := record[c.idField]

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
				// The watermark this row represents, so acknowledging it can
				// move the persisted position. Read must not move it: the
				// engine writes GetState down on every ack, so a position that
				// advanced here would already be past a message still in flight.
				if currentID != nil {
					msg.SetMetadata("clickhouse_last_id", fmt.Sprintf("%v", currentID))
				}

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

	rows, err := c.conn.Query(ctx, "SELECT * FROM "+quoted)
	if err != nil {
		return fmt.Errorf("failed to query table %q: %w", table, err)
	}
	defer rows.Close()

	for rows.Next() {
		record, err := scanRow(rows)
		if err != nil {
			return err
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
	for table, id := range c.ackedIDs {
		state["last_id:"+table] = fmt.Sprintf("%v", id)
	}
	return state
}

func (c *ClickHouseSource) SetState(state map[string]string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for k, v := range state {
		if after, ok := strings.CutPrefix(k, "last_id:"); ok {
			table := after
			// Both: the acknowledged position is where this source resumes, and
			// reading starts from there too. Rows after it were never delivered.
			c.lastIDs[table] = v
			c.ackedIDs[table] = v
		}
	}
}

// Ack moves the position a restart resumes from.
//
// It used to do nothing, while Read advanced the watermark the moment a row was
// fetched — so the position the engine persisted was always at least one row
// ahead of what had been delivered, and a worker that died with a message in
// flight came back past it. The row was never handed out again and nothing
// reported a gap.
//
// The same distinction the MongoDB and MySQL sources draw, for the same reason.
func (c *ClickHouseSource) Ack(ctx context.Context, msg hermod.Message) error {
	if msg == nil {
		return nil
	}
	id := msg.Metadata()["clickhouse_last_id"]
	if id == "" {
		return nil
	}
	table := msg.Table()
	if table == "" {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.ackedIDs[table] = id
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

	const query = "SELECT name, type, is_nullable = 'YES', is_in_primary_key, default_expression FROM system.columns WHERE table = ?"
	rows, err := c.conn.Query(ctx, query, table)
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

	record, err := scanRow(rows)
	if err != nil {
		return nil, err
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
