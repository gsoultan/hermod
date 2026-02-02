package scylladb

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

// ScyllaDBSource implements the hermod.Source interface for ScyllaDB.
type ScyllaDBSource struct {
	hosts        []string
	useCDC       bool
	tables       []string
	idField      string
	pollInterval time.Duration
	cluster      *gocql.ClusterConfig
	session      *gocql.Session
	mu           sync.Mutex
	logger       hermod.Logger
	lastIDs      map[string]interface{}
}

func NewScyllaDBSource(hosts []string, tables []string, idField string, pollInterval time.Duration, useCDC bool) *ScyllaDBSource {
	if pollInterval <= 0 {
		pollInterval = 5 * time.Second
	}
	return &ScyllaDBSource{
		hosts:        hosts,
		tables:       tables,
		idField:      idField,
		pollInterval: pollInterval,
		useCDC:       useCDC,
		lastIDs:      make(map[string]interface{}),
	}
}

func (s *ScyllaDBSource) SetLogger(logger hermod.Logger) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logger = logger
}

func (s *ScyllaDBSource) log(level, msg string, keysAndValues ...interface{}) {
	s.mu.Lock()
	logger := s.logger
	s.mu.Unlock()

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

func (s *ScyllaDBSource) init(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.session != nil && !s.session.Closed() {
		return nil
	}

	s.cluster = gocql.NewCluster(s.hosts...)
	s.cluster.Consistency = gocql.One
	s.cluster.Timeout = 5 * time.Second

	session, err := s.cluster.CreateSession()
	if err != nil {
		return fmt.Errorf("failed to connect to scylladb: %w", err)
	}
	s.session = session
	return nil
}

func (s *ScyllaDBSource) Read(ctx context.Context) (hermod.Message, error) {
	if err := s.init(ctx); err != nil {
		return nil, err
	}

	if !s.useCDC {
		<-ctx.Done()
		return nil, ctx.Err()
	}

	for {
		for _, table := range s.tables {
			s.mu.Lock()
			lastID := s.lastIDs[table]
			s.mu.Unlock()

			var query string
			var args []interface{}

			if lastID != nil && s.idField != "" {
				query = fmt.Sprintf("SELECT * FROM %s WHERE %s > ? LIMIT 1 ALLOW FILTERING", table, s.idField)
				args = append(args, lastID)
			} else {
				query = fmt.Sprintf("SELECT * FROM %s LIMIT 1", table)
			}

			iter := s.session.Query(query).WithContext(ctx).Iter()
			columns := iter.Columns()
			values := make([]interface{}, len(columns))
			for i := range values {
				values[i] = new(interface{})
			}

			if iter.Scan(values...) {
				record := make(map[string]interface{})
				var currentID interface{}
				for i, col := range columns {
					val := *(values[i].(*interface{}))
					if b, ok := val.([]byte); ok {
						val = string(b)
					}
					record[col.Name] = val
					if col.Name == s.idField {
						currentID = val
					}
				}

				if currentID != nil {
					s.mu.Lock()
					s.lastIDs[table] = currentID
					s.mu.Unlock()
				}

				iter.Close()

				afterJSON, _ := json.Marshal(message.SanitizeMap(record))
				msg := message.AcquireMessage()
				msg.SetID(fmt.Sprintf("scylladb-%s-%v", table, currentID))
				msg.SetOperation(hermod.OpCreate)
				msg.SetTable(table)
				msg.SetAfter(afterJSON)
				msg.SetMetadata("source", "scylladb")

				return msg, nil
			}
			iter.Close()
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(s.pollInterval):
		}
	}
}

func (s *ScyllaDBSource) GetState() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()

	state := make(map[string]string)
	for table, id := range s.lastIDs {
		state["last_id:"+table] = fmt.Sprintf("%v", id)
	}
	return state
}

func (s *ScyllaDBSource) SetState(state map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for k, v := range state {
		if strings.HasPrefix(k, "last_id:") {
			table := strings.TrimPrefix(k, "last_id:")
			s.lastIDs[table] = v
		}
	}
}

func (s *ScyllaDBSource) Ack(ctx context.Context, msg hermod.Message) error {
	return nil
}

func (s *ScyllaDBSource) Ping(ctx context.Context) error {
	if err := s.init(ctx); err != nil {
		return err
	}
	return s.session.Query("SELECT now() FROM system.local").WithContext(ctx).Exec()
}

func (s *ScyllaDBSource) Close() error {
	s.log("INFO", "Closing ScyllaDBSource")
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.session != nil {
		s.session.Close()
		s.session = nil
		return nil
	}
	return nil
}

func (s *ScyllaDBSource) DiscoverDatabases(ctx context.Context) ([]string, error) {
	if err := s.init(ctx); err != nil {
		return nil, err
	}

	iter := s.session.Query("SELECT keyspace_name FROM system_schema.keyspaces").WithContext(ctx).Iter()
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

func (s *ScyllaDBSource) DiscoverTables(ctx context.Context) ([]string, error) {
	if err := s.init(ctx); err != nil {
		return nil, err
	}

	iter := s.session.Query("SELECT keyspace_name, table_name FROM system_schema.tables").WithContext(ctx).Iter()
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

func (s *ScyllaDBSource) Sample(ctx context.Context, table string) (hermod.Message, error) {
	if err := s.init(ctx); err != nil {
		return nil, err
	}

	query := fmt.Sprintf("SELECT * FROM %s LIMIT 1", table)
	iter := s.session.Query(query).WithContext(ctx).Iter()

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
	msg.SetMetadata("source", "scylladb")
	msg.SetMetadata("sample", "true")

	return msg, nil
}
