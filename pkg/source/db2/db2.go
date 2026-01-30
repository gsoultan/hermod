package db2

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
	"github.com/user/hermod/pkg/sqlutil"
)

// DB2Source implements the hermod.Source interface for DB2.
// Note: Requires a DB2 driver like github.com/ibmdb/go_ibm_db to be registered.
type DB2Source struct {
	connString string
	useCDC     bool
	db         *sql.DB
	mu         sync.Mutex
	logger     hermod.Logger
}

func NewDB2Source(connString string, useCDC bool) *DB2Source {
	return &DB2Source{
		connString: connString,
		useCDC:     useCDC,
	}
}

func (d *DB2Source) SetLogger(logger hermod.Logger) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.logger = logger
}

func (d *DB2Source) log(level, msg string, keysAndValues ...interface{}) {
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
	defer d.mu.Unlock()

	if d.db != nil {
		return nil
	}

	// Using the standard driver name for IBM DB2
	db, err := sql.Open("go_ibm_db", d.connString)
	if err != nil {
		return fmt.Errorf("failed to open db2 connection (ensure go_ibm_db driver is installed): %w", err)
	}
	d.db = db
	return d.db.PingContext(ctx)
}

func (d *DB2Source) Read(ctx context.Context) (hermod.Message, error) {
	if err := d.init(ctx); err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(5 * time.Second):
		return nil, nil
	}
}

func (d *DB2Source) Ack(ctx context.Context, msg hermod.Message) error {
	return nil
}

func (d *DB2Source) Ping(ctx context.Context) error {
	if err := d.init(ctx); err != nil {
		return err
	}
	return d.db.PingContext(ctx)
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

	// In DB2, databases are often fixed by connection, but we can list schemas.
	rows, err := d.db.QueryContext(ctx, "SELECT SCHEMANAME FROM SYSCAT.SCHEMATA")
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

	rows, err := d.db.QueryContext(ctx, "SELECT TABSCHEMA || '.' || TABNAME FROM SYSCAT.TABLES WHERE TABSCHEMA NOT LIKE 'SYS%'")
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

	quoted, err := sqlutil.QuoteIdent("db2", table)
	if err != nil {
		return nil, fmt.Errorf("invalid table name: %w", err)
	}
	rows, err := d.db.QueryContext(ctx, fmt.Sprintf("SELECT * FROM %s FETCH FIRST 1 ROWS ONLY", quoted))
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
	msg.SetMetadata("source", "db2")
	msg.SetMetadata("sample", "true")

	return msg, nil
}
