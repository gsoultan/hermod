package db2

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/gsoultan/Hermod"
	"github.com/gsoultan/Hermod/pkg/comm/message"
	sourcebuf "github.com/gsoultan/Hermod/pkg/comm/source"
	"github.com/gsoultan/Hermod/pkg/infra/sqlutil"
)

// DB2Source implements the hermod.Source interface for DB2.
// Note: Requires a DB2 driver like github.com/ibmdb/go_ibm_db to be registered.
type DB2Source struct {
	connString   string
	useCDC       bool
	tables       []string
	idField      string
	pollInterval time.Duration
	db           *sql.DB
	mu           sync.Mutex
	logger       hermod.Logger
	lastIDs      map[string]any
	// pendingIDs is the furthest row handed to a caller, acknowledged or
	// not. It keeps a running poll moving forward; only lastIDs is persisted.
	pendingIDs map[string]any
	msgChan    chan hermod.Message
}

func NewDB2Source(connString string, tables []string, idField string, pollInterval time.Duration, useCDC bool) *DB2Source {
	if pollInterval <= 0 {
		pollInterval = 5 * time.Second
	}
	return &DB2Source{
		connString:   connString,
		tables:       tables,
		idField:      idField,
		pollInterval: pollInterval,
		useCDC:       useCDC,
		lastIDs:      make(map[string]any),
		pendingIDs:   make(map[string]any),
		msgChan:      make(chan hermod.Message, sourcebuf.DefaultSourceBuffer),
	}
}

func (d *DB2Source) SetLogger(logger hermod.Logger) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.logger = logger
}

func (d *DB2Source) log(level, msg string, keysAndValues ...any) {
	d.mu.Lock()
	logger := d.logger
	d.mu.Unlock()

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

func (d *DB2Source) init(ctx context.Context) error {
	d.mu.Lock()
	if d.db != nil {
		d.mu.Unlock()
		return nil
	}
	d.mu.Unlock()

	// Using the standard driver name for IBM DB2
	db, err := sql.Open("go_ibm_db", d.connString)
	if err != nil {
		return fmt.Errorf("failed to open db2 connection (ensure go_ibm_db driver is installed): %w", err)
	}

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return err
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if d.db != nil {
		db.Close()
		return nil
	}
	d.db = db
	return nil
}

func (d *DB2Source) Read(ctx context.Context) (hermod.Message, error) {
	if err := d.init(ctx); err != nil {
		return nil, err
	}

	if !d.useCDC {
		select {
		case msg := <-d.msgChan:
			return msg, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	for {
		select {
		case msg := <-d.msgChan:
			return msg, nil
		default:
		}

		for _, table := range d.tables {
			d.mu.Lock()
			// Poll from the furthest row handed out, acknowledged or not, so a

			// single process does not re-read what it is already carrying. The

			// *persisted* cursor is lastIDs, moved only by Ack.

			lastID := d.pendingIDs[table]

			if lastID == nil {

				lastID = d.lastIDs[table]

			}
			d.mu.Unlock()

			var query string
			var args []any
			var err error

			if lastID != nil && d.idField != "" {
				query, err = sqlutil.BuildIncrementalQuery("db2", table, d.idField)
				if err != nil {
					return nil, err
				}
				args = append(args, lastID)
			} else {
				query, err = sqlutil.BuildFirstRowQuery("db2", table)
				if err != nil {
					return nil, err
				}
			}

			rows, err := d.db.QueryContext(ctx, query, args...)
			if err != nil {
				return nil, fmt.Errorf("db2 poll error: %w", err)
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
					if col == d.idField {
						currentID = val
					}
				}

				if currentID != nil {
					d.mu.Lock()
					d.pendingIDs[table] = currentID
					d.mu.Unlock()
				}

				afterJSON, _ := json.Marshal(message.SanitizeMap(record))
				msg := message.AcquireMessage()
				msg.SetID(fmt.Sprintf("db2-%s-%v", table, currentID))
				msg.SetOperation(hermod.OpCreate)
				msg.SetTable(table)
				msg.SetAfter(afterJSON)
				msg.SetMetadata("source", "db2")
				if currentID != nil {
					msg.SetMetadata(watermarkKey, fmt.Sprintf("%v", currentID))
					msg.SetMetadata(watermarkTableKey, table)
				}

				return msg, nil
			}
			rows.Close()
		}

		select {
		case msg := <-d.msgChan:
			return msg, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(d.pollInterval):
		}
	}
}

func (d *DB2Source) Snapshot(ctx context.Context, tables ...string) error {
	if err := d.init(ctx); err != nil {
		return err
	}

	targetTables := tables
	if len(targetTables) == 0 {
		targetTables = d.tables
	}

	if len(targetTables) == 0 {
		var err error
		targetTables, err = d.DiscoverTables(ctx)
		if err != nil {
			return err
		}
	}

	for _, table := range targetTables {
		if err := d.snapshotTable(ctx, table); err != nil {
			return err
		}
	}
	return nil
}

func (d *DB2Source) snapshotTable(ctx context.Context, table string) error {
	quoted, err := sqlutil.QuoteIdent("db2", table)
	if err != nil {
		return fmt.Errorf("invalid table name %q: %w", table, err)
	}

	rows, err := d.db.QueryContext(ctx, "SELECT * FROM "+quoted)
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
		msg.SetMetadata("source", "db2")
		msg.SetMetadata("snapshot", "true")

		select {
		case d.msgChan <- msg:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return rows.Err()
}

func (d *DB2Source) GetState() map[string]string {
	d.mu.Lock()
	defer d.mu.Unlock()

	state := make(map[string]string)
	for table, id := range d.lastIDs {
		state["last_id:"+table] = fmt.Sprintf("%v", id)
	}
	return state
}

func (d *DB2Source) SetState(state map[string]string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	for k, v := range state {
		if after, ok := strings.CutPrefix(k, "last_id:"); ok {
			table := after
			d.lastIDs[table] = v
		}
	}
}

// watermarkKey and watermarkTableKey carry the row's incremental value and
// the table it came from, so an Ack can move the cursor without the source
// having to guess which read it belonged to.
const (
	watermarkKey      = "db2_last_id"
	watermarkTableKey = "db2_table"
)

// Ack moves the persisted cursor to the acknowledged row. It must not move on
// read: GetState is the engine's persistence contract, so a cursor advanced
// when a row is handed out is already past rows still in flight, and a crash
// before the sinks wrote them erases them from the resume.
func (d *DB2Source) Ack(ctx context.Context, msg hermod.Message) error {
	// The conformance suite feeds every source a nil ack, because a worker
	// goroutine dereferencing one takes the engine down.
	if msg == nil {
		return nil
	}
	md := msg.Metadata()
	id, table := md[watermarkKey], md[watermarkTableKey]
	if id == "" || table == "" {
		return nil
	}
	d.mu.Lock()
	d.lastIDs[table] = id
	d.mu.Unlock()
	return nil
}

func (d *DB2Source) Ping(ctx context.Context) error {
	if err := d.init(ctx); err != nil {
		return err
	}

	d.mu.Lock()
	db := d.db
	d.mu.Unlock()
	if db == nil {
		return fmt.Errorf("db2 connection not initialized")
	}

	return db.PingContext(ctx)
}

func (d *DB2Source) Close() error {
	d.log("INFO", "Closing DB2Source")
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.db != nil {
		err := d.db.Close()
		d.db = nil
		return err
	}
	return nil
}

func (d *DB2Source) DiscoverDatabases(ctx context.Context) ([]string, error) {
	if err := d.init(ctx); err != nil {
		return nil, err
	}

	d.mu.Lock()
	db := d.db
	d.mu.Unlock()
	if db == nil {
		return nil, fmt.Errorf("db2 connection not initialized")
	}

	// In DB2, databases are often fixed by connection, but we can list schemas.
	rows, err := db.QueryContext(ctx, "SELECT SCHEMANAME FROM SYSCAT.SCHEMATA")
	if err != nil {
		return nil, fmt.Errorf("failed to query schemas: %w", err)
	}
	defer rows.Close()

	var schemas []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		schemas = append(schemas, name)
	}
	return schemas, nil
}

func (d *DB2Source) DiscoverTables(ctx context.Context) ([]string, error) {
	if err := d.init(ctx); err != nil {
		return nil, err
	}

	d.mu.Lock()
	db := d.db
	d.mu.Unlock()
	if db == nil {
		return nil, fmt.Errorf("db2 connection not initialized")
	}

	rows, err := db.QueryContext(ctx, "SELECT TABSCHEMA || '.' || TABNAME FROM SYSCAT.TABLES WHERE TABSCHEMA NOT LIKE 'SYS%'")
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

func (d *DB2Source) Sample(ctx context.Context, table string) (hermod.Message, error) {
	if err := d.init(ctx); err != nil {
		return nil, err
	}

	d.mu.Lock()
	db := d.db
	d.mu.Unlock()
	if db == nil {
		return nil, fmt.Errorf("db2 connection not initialized")
	}

	quoted, err := sqlutil.QuoteIdent("db2", table)
	if err != nil {
		return nil, fmt.Errorf("invalid table name: %w", err)
	}
	rows, err := db.QueryContext(ctx, fmt.Sprintf("SELECT * FROM %s FETCH FIRST 1 ROWS ONLY", quoted))
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
	msg.SetMetadata("source", "db2")
	msg.SetMetadata("sample", "true")

	return msg, nil
}
