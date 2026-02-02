package yugabyte

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
	"github.com/user/hermod/pkg/sqlutil"
)

// YugabyteSource implements the hermod.Source interface for YugabyteDB.
// Since YugabyteDB is PostgreSQL-compatible, it uses pgx.
type YugabyteSource struct {
	connString   string
	useCDC       bool
	tables       []string
	idField      string
	pollInterval time.Duration
	conn         *pgx.Conn
	mu           sync.Mutex
	logger       hermod.Logger
	lastIDs      map[string]interface{}
}

func NewYugabyteSource(connString string, tables []string, idField string, pollInterval time.Duration, useCDC bool) *YugabyteSource {
	if pollInterval <= 0 {
		pollInterval = 5 * time.Second
	}
	return &YugabyteSource{
		connString:   connString,
		tables:       tables,
		idField:      idField,
		pollInterval: pollInterval,
		useCDC:       useCDC,
		lastIDs:      make(map[string]interface{}),
	}
}

func (y *YugabyteSource) SetLogger(logger hermod.Logger) {
	y.mu.Lock()
	defer y.mu.Unlock()
	y.logger = logger
}

func (y *YugabyteSource) log(level, msg string, keysAndValues ...interface{}) {
	y.mu.Lock()
	logger := y.logger
	y.mu.Unlock()

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

func (y *YugabyteSource) init(ctx context.Context) error {
	y.mu.Lock()
	defer y.mu.Unlock()

	if y.conn != nil && !y.conn.IsClosed() {
		return nil
	}

	conn, err := pgx.Connect(ctx, y.connString)
	if err != nil {
		return fmt.Errorf("failed to connect to yugabyte: %w", err)
	}
	y.conn = conn
	return nil
}

func (y *YugabyteSource) Read(ctx context.Context) (hermod.Message, error) {
	if err := y.init(ctx); err != nil {
		return nil, err
	}

	if !y.useCDC {
		<-ctx.Done()
		return nil, ctx.Err()
	}

	for {
		for _, table := range y.tables {
			y.mu.Lock()
			lastID := y.lastIDs[table]
			y.mu.Unlock()

			quotedTable, err := sqlutil.QuoteIdent("pgx", table)
			if err != nil {
				return nil, err
			}

			var query string
			var args []interface{}

			if lastID != nil && y.idField != "" {
				quotedID, _ := sqlutil.QuoteIdent("pgx", y.idField)
				query = fmt.Sprintf("SELECT * FROM %s WHERE %s > $1 ORDER BY %s ASC LIMIT 1", quotedTable, quotedID, quotedID)
				args = append(args, lastID)
			} else {
				query = fmt.Sprintf("SELECT * FROM %s LIMIT 1", quotedTable)
			}

			rows, err := y.conn.Query(ctx, query, args...)
			if err != nil {
				return nil, fmt.Errorf("yugabyte poll error: %w", err)
			}

			if rows.Next() {
				fields := rows.FieldDescriptions()
				values, err := rows.Values()
				if err != nil {
					rows.Close()
					return nil, err
				}
				rows.Close()

				record := make(map[string]interface{})
				var currentID interface{}
				for i, field := range fields {
					val := values[i]
					if b, ok := val.([]byte); ok {
						val = string(b)
					}
					record[field.Name] = val
					if field.Name == y.idField {
						currentID = val
					}
				}

				if currentID != nil {
					y.mu.Lock()
					y.lastIDs[table] = currentID
					y.mu.Unlock()
				}

				afterJSON, _ := json.Marshal(message.SanitizeMap(record))
				msg := message.AcquireMessage()
				msg.SetID(fmt.Sprintf("yugabyte-%s-%v", table, currentID))
				msg.SetOperation(hermod.OpCreate)
				msg.SetTable(table)
				msg.SetAfter(afterJSON)
				msg.SetMetadata("source", "yugabyte")

				return msg, nil
			}
			rows.Close()
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(y.pollInterval):
		}
	}
}

func (y *YugabyteSource) GetState() map[string]string {
	y.mu.Lock()
	defer y.mu.Unlock()

	state := make(map[string]string)
	for table, id := range y.lastIDs {
		state["last_id:"+table] = fmt.Sprintf("%v", id)
	}
	return state
}

func (y *YugabyteSource) SetState(state map[string]string) {
	y.mu.Lock()
	defer y.mu.Unlock()

	for k, v := range state {
		if strings.HasPrefix(k, "last_id:") {
			table := strings.TrimPrefix(k, "last_id:")
			y.lastIDs[table] = v
		}
	}
}

func (y *YugabyteSource) Ack(ctx context.Context, msg hermod.Message) error {
	return nil
}

func (y *YugabyteSource) Ping(ctx context.Context) error {
	if err := y.init(ctx); err != nil {
		return err
	}
	return y.conn.Ping(ctx)
}

func (y *YugabyteSource) Close() error {
	y.log("INFO", "Closing YugabyteSource")
	y.mu.Lock()
	defer y.mu.Unlock()

	if y.conn != nil {
		err := y.conn.Close(context.Background())
		y.conn = nil
		return err
	}
	return nil
}

func (y *YugabyteSource) DiscoverDatabases(ctx context.Context) ([]string, error) {
	if err := y.init(ctx); err != nil {
		return nil, err
	}

	rows, err := y.conn.Query(ctx, "SELECT datname FROM pg_database WHERE datistemplate = false")
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

func (y *YugabyteSource) DiscoverTables(ctx context.Context) ([]string, error) {
	if err := y.init(ctx); err != nil {
		return nil, err
	}

	rows, err := y.conn.Query(ctx, "SELECT schemaname || '.' || tablename FROM pg_catalog.pg_tables WHERE schemaname NOT IN ('pg_catalog', 'information_schema')")
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

func (y *YugabyteSource) Sample(ctx context.Context, table string) (hermod.Message, error) {
	if err := y.init(ctx); err != nil {
		return nil, err
	}

	quoted, err := sqlutil.QuoteIdent("pgx", table)
	if err != nil {
		return nil, fmt.Errorf("invalid table name: %w", err)
	}
	rows, err := y.conn.Query(ctx, fmt.Sprintf("SELECT * FROM %s LIMIT 1", quoted))
	if err != nil {
		return nil, fmt.Errorf("failed to query sample record: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		return nil, fmt.Errorf("no records found in table %s", table)
	}

	fields := rows.FieldDescriptions()
	values, err := rows.Values()
	if err != nil {
		return nil, err
	}

	record := make(map[string]interface{})
	for i, field := range fields {
		val := values[i]
		if b, ok := val.([]byte); ok {
			record[field.Name] = string(b)
		} else {
			record[field.Name] = val
		}
	}

	afterJSON, _ := json.Marshal(message.SanitizeMap(record))

	msg := message.AcquireMessage()
	msg.SetID(fmt.Sprintf("sample-%s-%d", table, time.Now().Unix()))
	msg.SetOperation(hermod.OpSnapshot)
	msg.SetTable(table)
	msg.SetAfter(afterJSON)
	msg.SetMetadata("source", "yugabyte")
	msg.SetMetadata("sample", "true")

	return msg, nil
}
