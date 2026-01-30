package cassandra

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/gocql/gocql"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

// CassandraSource implements the hermod.Source interface for Cassandra.
type CassandraSource struct {
	hosts   []string
	useCDC  bool
	cluster *gocql.ClusterConfig
	session *gocql.Session
	mu      sync.Mutex
	logger  hermod.Logger
}

func NewCassandraSource(hosts []string, useCDC bool) *CassandraSource {
	return &CassandraSource{
		hosts:  hosts,
		useCDC: useCDC,
	}
}

func (c *CassandraSource) SetLogger(logger hermod.Logger) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.logger = logger
}

func (c *CassandraSource) log(level, msg string, keysAndValues ...interface{}) {
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

	// Cassandra Read blocks for now.
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(5 * time.Second):
		return nil, nil
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

func (c *CassandraSource) Sample(ctx context.Context, table string) (hermod.Message, error) {
	if err := c.init(ctx); err != nil {
		return nil, err
	}

	// table here might be "keyspace.table" or just "table"
	query := fmt.Sprintf("SELECT * FROM %s LIMIT 1", table)
	iter := c.session.Query(query).WithContext(ctx).Iter()

	columns := iter.Columns()
	values := make([]interface{}, len(columns))
	for i := range values {
		values[i] = new(interface{})
	}

	if !iter.Scan(values...) {
		iter.Close()
		return nil, fmt.Errorf("no records found in table %s", table)
	}

	record := make(map[string]interface{})
	for i, col := range columns {
		val := *(values[i].(*interface{}))
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
