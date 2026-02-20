package cassandra

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/gocql/gocql"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

// CassandraSource implements the hermod.Source interface for Cassandra.
type CassandraSource struct {
	hosts        []string
	useCDC       bool
	tables       []string
	idField      string
	pollInterval time.Duration
	cluster      *gocql.ClusterConfig
	session      *gocql.Session
	mu           sync.Mutex
	logger       hermod.Logger
	lastIDs      map[string]any
}

func NewCassandraSource(hosts []string, tables []string, idField string, pollInterval time.Duration, useCDC bool) *CassandraSource {
	if pollInterval <= 0 {
		pollInterval = 5 * time.Second
	}
	return &CassandraSource{
		hosts:        hosts,
		tables:       tables,
		idField:      idField,
		pollInterval: pollInterval,
		useCDC:       useCDC,
		lastIDs:      make(map[string]any),
	}
}

func (c *CassandraSource) SetLogger(logger hermod.Logger) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.logger = logger
}

func (c *CassandraSource) log(level, msg string, keysAndValues ...any) {
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

func (c *CassandraSource) init(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.session != nil && !c.session.Closed() {
		return nil
	}

	c.cluster = gocql.NewCluster(c.hosts...)
	c.cluster.Consistency = gocql.One
	c.cluster.Timeout = 5 * time.Second

	session, err := c.cluster.CreateSession()
	if err != nil {
		return fmt.Errorf("failed to connect to cassandra: %w", err)
	}
	c.session = session
	return nil
}

func (c *CassandraSource) Read(ctx context.Context) (hermod.Message, error) {
	if err := c.init(ctx); err != nil {
		return nil, err
	}

	if !c.useCDC {
		<-ctx.Done()
		return nil, ctx.Err()
	}

	for {
		for _, table := range c.tables {
			c.mu.Lock()
			lastID := c.lastIDs[table]
			c.mu.Unlock()

			var query string
			var args []any

			if lastID != nil && c.idField != "" {
				// Cassandra doesn't support > on all types easily without ALLOW FILTERING or specific indexing
				// But for polling we assume it's an incremental field (like a timestamp or counter)
				query = fmt.Sprintf("SELECT * FROM %s WHERE %s > ? LIMIT 1 ALLOW FILTERING", table, c.idField)
				args = append(args, lastID)
			} else {
				query = fmt.Sprintf("SELECT * FROM %s LIMIT 1", table)
			}

			iter := c.session.Query(query).WithContext(ctx).Iter()
			columns := iter.Columns()
			values := make([]any, len(columns))
			for i := range values {
				values[i] = new(any)
			}

			if iter.Scan(values...) {
				record := make(map[string]any)
				var currentID any
				for i, col := range columns {
					val := *(values[i].(*any))
					if b, ok := val.([]byte); ok {
						val = string(b)
					}
					record[col.Name] = val
					if col.Name == c.idField {
						currentID = val
					}
				}

				if currentID != nil {
					c.mu.Lock()
					c.lastIDs[table] = currentID
					c.mu.Unlock()
				}

				iter.Close()

				afterJSON, _ := json.Marshal(message.SanitizeMap(record))
				msg := message.AcquireMessage()
				msg.SetID(fmt.Sprintf("cassandra-%s-%v", table, currentID))
				msg.SetOperation(hermod.OpCreate)
				msg.SetTable(table)
				msg.SetAfter(afterJSON)
				msg.SetMetadata("source", "cassandra")

				return msg, nil
			}
			iter.Close()
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(c.pollInterval):
		}
	}
}

func (c *CassandraSource) GetState() map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()

	state := make(map[string]string)
	for table, id := range c.lastIDs {
		state["last_id:"+table] = fmt.Sprintf("%v", id)
	}
	return state
}

func (c *CassandraSource) SetState(state map[string]string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for k, v := range state {
		if strings.HasPrefix(k, "last_id:") {
			table := strings.TrimPrefix(k, "last_id:")
			c.lastIDs[table] = v
		}
	}
}

func (c *CassandraSource) Ack(ctx context.Context, msg hermod.Message) error {
	return nil
}

func (c *CassandraSource) Ping(ctx context.Context) error {
	if err := c.init(ctx); err != nil {
		return err
	}
	return c.session.Query("SELECT now() FROM system.local").WithContext(ctx).Exec()
}

func (c *CassandraSource) Close() error {
	c.log("INFO", "Closing CassandraSource")
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.session != nil {
		c.session.Close()
		c.session = nil
		return nil
	}
	return nil
}

func (c *CassandraSource) DiscoverDatabases(ctx context.Context) ([]string, error) {
	if err := c.init(ctx); err != nil {
		return nil, err
	}

	iter := c.session.Query("SELECT keyspace_name FROM system_schema.keyspaces").WithContext(ctx).Iter()
	var keyspaces []string
	var name string
	for iter.Scan(&name) {
		keyspaces = append(keyspaces, name)
	}
	if err := iter.Close(); err != nil {
		return nil, fmt.Errorf("failed to query keyspaces: %w", err)
	}
	return keyspaces, nil
}

func (c *CassandraSource) DiscoverTables(ctx context.Context) ([]string, error) {
	if err := c.init(ctx); err != nil {
		return nil, err
	}

	// This queries all tables across all keyspaces.
	// In a real scenario, we might want to filter by keyspace.
	iter := c.session.Query("SELECT keyspace_name, table_name FROM system_schema.tables").WithContext(ctx).Iter()
	var tables []string
	var ks, tbl string
	for iter.Scan(&ks, &tbl) {
		tables = append(tables, ks+"."+tbl)
	}
	if err := iter.Close(); err != nil {
		return nil, fmt.Errorf("failed to query tables: %w", err)
	}
	return tables, nil
}

func (c *CassandraSource) DiscoverColumns(ctx context.Context, table string) ([]hermod.ColumnInfo, error) {
	if err := c.init(ctx); err != nil {
		return nil, err
	}

	targetKS := ""
	targetTable := table
	if strings.Contains(table, ".") {
		parts := strings.SplitN(table, ".", 2)
		targetKS = parts[0]
		targetTable = parts[1]
	}

	query := "SELECT column_name, type, kind FROM system_schema.columns WHERE table_name = ?"
	args := []any{targetTable}
	if targetKS != "" {
		query += " AND keyspace_name = ?"
		args = append(args, targetKS)
	}

	iter := c.session.Query(query, args...).WithContext(ctx).Iter()
	var columns []hermod.ColumnInfo
	var name, ctype, kind string
	for iter.Scan(&name, &ctype, &kind) {
		columns = append(columns, hermod.ColumnInfo{
			Name:       name,
			Type:       ctype,
			IsPK:       kind == "partition_key" || kind == "clustering",
			IsNullable: kind != "partition_key" && kind != "clustering",
			IsIdentity: false,
		})
	}
	if err := iter.Close(); err != nil {
		return nil, fmt.Errorf("failed to query columns: %w", err)
	}
	return columns, nil
}

func (c *CassandraSource) Sample(ctx context.Context, table string) (hermod.Message, error) {
	if err := c.init(ctx); err != nil {
		return nil, err
	}

	// table here might be "keyspace.table" or just "table"
	query := fmt.Sprintf("SELECT * FROM %s LIMIT 1", table)
	iter := c.session.Query(query).WithContext(ctx).Iter()

	columns := iter.Columns()
	values := make([]any, len(columns))
	for i := range values {
		values[i] = new(any)
	}

	if !iter.Scan(values...) {
		iter.Close()
		return nil, fmt.Errorf("no records found in table %s", table)
	}

	record := make(map[string]any)
	for i, col := range columns {
		val := *(values[i].(*any))
		if b, ok := val.([]byte); ok {
			record[col.Name] = string(b)
		} else {
			record[col.Name] = val
		}
	}

	if err := iter.Close(); err != nil {
		return nil, err
	}

	afterJSON, _ := json.Marshal(message.SanitizeMap(record))

	msg := message.AcquireMessage()
	msg.SetID(fmt.Sprintf("sample-%s-%d", table, time.Now().Unix()))
	msg.SetOperation(hermod.OpSnapshot)
	msg.SetTable(table)
	msg.SetAfter(afterJSON)
	msg.SetMetadata("source", "cassandra")
	msg.SetMetadata("sample", "true")

	return msg, nil
}
