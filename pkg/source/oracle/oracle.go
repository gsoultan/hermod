package oracle

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	_ "github.com/sijms/go-ora/v2"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
	"github.com/user/hermod/pkg/sqlutil"
)

// OracleSource implements the hermod.Source interface for Oracle.
type OracleSource struct {
	connString   string
	useCDC       bool
	tables       []string
	idField      string
	pollInterval time.Duration
	db           *sql.DB
	mu           sync.Mutex
	logger       hermod.Logger
	lastIDs      map[string]interface{}
}

func NewOracleSource(connString string, tables []string, idField string, pollInterval time.Duration, useCDC bool) *OracleSource {
	if pollInterval <= 0 {
		pollInterval = 5 * time.Second
	}
	return &OracleSource{
		connString:   connString,
		tables:       tables,
		idField:      idField,
		pollInterval: pollInterval,
		useCDC:       useCDC,
		lastIDs:      make(map[string]interface{}),
	}
}

func (o *OracleSource) SetLogger(logger hermod.Logger) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.logger = logger
}

func (o *OracleSource) log(level, msg string, keysAndValues ...interface{}) {
	o.mu.Lock()
	logger := o.logger
	o.mu.Unlock()

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

func (o *OracleSource) init(ctx context.Context) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.db != nil {
		return nil
	}

	db, err := sql.Open("oracle", o.connString)
	if err != nil {
		return fmt.Errorf("failed to connect to oracle: %w", err)
	}
	o.db = db
	return o.db.PingContext(ctx)
}

func (o *OracleSource) Read(ctx context.Context) (hermod.Message, error) {
	if err := o.init(ctx); err != nil {
		return nil, err
	}

	if !o.useCDC {
		<-ctx.Done()
		return nil, ctx.Err()
	}

	for {
		for _, table := range o.tables {
			o.mu.Lock()
			lastID := o.lastIDs[table]
			o.mu.Unlock()

			quotedTable, err := sqlutil.QuoteIdent("oracle", table)
			if err != nil {
				return nil, err
			}

			var query string
			var args []interface{}

			if lastID != nil && o.idField != "" {
				quotedID, _ := sqlutil.QuoteIdent("oracle", o.idField)
				// Oracle uses :1, :2 for placeholders in some drivers, but go-ora supports ? or :1
				// Hermod's sqlutil should handle this if we want to be very portable.
				// For now, let's use the standard ? if supported or adjust.
				query = fmt.Sprintf("SELECT * FROM %s WHERE %s > ? AND ROWNUM <= 1 ORDER BY %s ASC", quotedTable, quotedID, quotedID)
				args = append(args, lastID)
			} else {
				query = fmt.Sprintf("SELECT * FROM %s WHERE ROWNUM <= 1", quotedTable)
			}

			rows, err := o.db.QueryContext(ctx, query, args...)
			if err != nil {
				return nil, fmt.Errorf("oracle poll error: %w", err)
			}

			if rows.Next() {
				cols, _ := rows.Columns()
				values := make([]interface{}, len(cols))
				ptr := make([]interface{}, len(cols))
				for i := range values {
					ptr[i] = &values[i]
				}

				if err := rows.Scan(ptr...); err != nil {
					rows.Close()
					return nil, err
				}
				rows.Close()

				record := make(map[string]interface{})
				var currentID interface{}
				for i, col := range cols {
					val := values[i]
					if b, ok := val.([]byte); ok {
						val = string(b)
					}
					record[col] = val
					if col == o.idField {
						currentID = val
					}
				}

				if currentID != nil {
					o.mu.Lock()
					o.lastIDs[table] = currentID
					o.mu.Unlock()
				}

				afterJSON, _ := json.Marshal(message.SanitizeMap(record))
				msg := message.AcquireMessage()
				msg.SetID(fmt.Sprintf("oracle-%s-%v", table, currentID))
				msg.SetOperation(hermod.OpCreate)
				msg.SetTable(table)
				msg.SetAfter(afterJSON)
				msg.SetMetadata("source", "oracle")

				return msg, nil
			}
			rows.Close()
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(o.pollInterval):
		}
	}
}

func (o *OracleSource) GetState() map[string]string {
	o.mu.Lock()
	defer o.mu.Unlock()

	state := make(map[string]string)
	for table, id := range o.lastIDs {
		state["last_id:"+table] = fmt.Sprintf("%v", id)
	}
	return state
}

func (o *OracleSource) SetState(state map[string]string) {
	o.mu.Lock()
	defer o.mu.Unlock()

	for k, v := range state {
		if strings.HasPrefix(k, "last_id:") {
			table := strings.TrimPrefix(k, "last_id:")
			o.lastIDs[table] = v
		}
	}
}

func (o *OracleSource) Ack(ctx context.Context, msg hermod.Message) error {
	return nil
}

func (o *OracleSource) Ping(ctx context.Context) error {
	if err := o.init(ctx); err != nil {
		return err
	}
	return o.db.PingContext(ctx)
}

func (o *OracleSource) Close() error {
	o.log("INFO", "Closing OracleSource")
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.db != nil {
		err := o.db.Close()
		o.db = nil
		return err
	}
	return nil
}

func (o *OracleSource) DiscoverDatabases(ctx context.Context) ([]string, error) {
	if err := o.init(ctx); err != nil {
		return nil, err
	}

	// In Oracle, we usually list users/schemas.
	rows, err := o.db.QueryContext(ctx, "SELECT username FROM all_users ORDER BY username")
	if err != nil {
		return nil, fmt.Errorf("failed to query users/databases: %w", err)
	}
	defer rows.Close()

	var users []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		users = append(users, name)
	}
	return users, nil
}

func (o *OracleSource) DiscoverTables(ctx context.Context) ([]string, error) {
	if err := o.init(ctx); err != nil {
		return nil, err
	}

	rows, err := o.db.QueryContext(ctx, "SELECT owner || '.' || table_name FROM all_tables WHERE owner NOT IN ('SYS', 'SYSTEM') ORDER BY owner, table_name")
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

func (o *OracleSource) Sample(ctx context.Context, table string) (hermod.Message, error) {
	if err := o.init(ctx); err != nil {
		return nil, err
	}

	// Oracle doesn't have LIMIT, but has ROWNUM or FETCH FIRST 1 ROWS ONLY
	quoted, err := sqlutil.QuoteIdent("oracle", table)
	if err != nil {
		return nil, fmt.Errorf("invalid table name: %w", err)
	}
	rows, err := o.db.QueryContext(ctx, fmt.Sprintf("SELECT * FROM %s WHERE ROWNUM <= 1", quoted))
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
	msg.SetMetadata("source", "oracle")
	msg.SetMetadata("sample", "true")

	return msg, nil
}
