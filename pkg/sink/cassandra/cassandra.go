package cassandra

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/gocql/gocql"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/evaluator"
	"github.com/user/hermod/pkg/sqlutil"
)

// CassandraSink implements the hermod.Sink interface for Cassandra.
type CassandraSink struct {
	hosts            []string
	keyspace         string
	session          *gocql.Session
	mu               sync.Mutex
	verifiedTables   sync.Map
	tableName        string
	mappings         []sqlutil.ColumnMapping
	useExistingTable bool
	deleteStrategy   string
	softDeleteColumn string
	softDeleteValue  string
	operationMode    string
	autoTruncate     bool
	autoSync         bool
}

func NewCassandraSink(hosts []string, keyspace string, tableName string, mappings []sqlutil.ColumnMapping, useExistingTable bool, deleteStrategy string, softDeleteColumn string, softDeleteValue string, operationMode string, autoTruncate bool, autoSync bool) *CassandraSink {
	if operationMode == "" {
		operationMode = "auto"
	}
	return &CassandraSink{
		hosts:            hosts,
		keyspace:         keyspace,
		tableName:        tableName,
		mappings:         mappings,
		useExistingTable: useExistingTable,
		deleteStrategy:   deleteStrategy,
		softDeleteColumn: softDeleteColumn,
		softDeleteValue:  softDeleteValue,
		operationMode:    operationMode,
		autoTruncate:     autoTruncate,
		autoSync:         autoSync,
	}
}

func (s *CassandraSink) Write(ctx context.Context, msg hermod.Message) error {
	return s.WriteBatch(ctx, []hermod.Message{msg})
}

func (s *CassandraSink) WriteBatch(ctx context.Context, msgs []hermod.Message) error {
	if len(msgs) == 0 {
		return nil
	}
	if s.session == nil {
		if err := s.init(); err != nil {
			return err
		}
	}

	// Group by table
	groups := make(map[string][]hermod.Message)
	for _, msg := range msgs {
		if msg == nil {
			continue
		}
		table := s.tableName
		if table == "" {
			table = msg.Table()
		}
		groups[table] = append(groups[table], msg)
	}

	for table, group := range groups {
		if err := s.ensureTable(ctx, table); err != nil {
			return fmt.Errorf("ensure table %s: %w", table, err)
		}

		batch := s.session.NewBatch(gocql.LoggedBatch).WithContext(ctx)
		for _, msg := range group {
			op := msg.Operation()
			if s.operationMode != "auto" && s.operationMode != "" {
				switch s.operationMode {
				case "insert", "upsert", "update":
					op = hermod.OpCreate
				case "delete":
					op = hermod.OpDelete
				}
			}

			if op == "" {
				op = hermod.OpCreate
			}

			if op == hermod.OpDelete {
				if s.deleteStrategy == "ignore" {
					continue
				}
				if len(s.mappings) > 0 {
					if err := s.deleteMapped(ctx, batch, table, msg); err != nil {
						return err
					}
				} else {
					query := fmt.Sprintf("DELETE FROM %s.%s WHERE id = ?", s.keyspace, table)
					batch.Query(query, msg.ID())
				}
				continue
			}

			if len(s.mappings) > 0 {
				data := msg.Data()
				if data == nil {
					_ = json.Unmarshal(msg.Payload(), &data)
				}
				var cols []string
				var placeholders []string
				var args []any
				for _, m := range s.mappings {
					val := evaluator.GetMsgValByPath(msg, m.SourceField)

					if m.IsIdentity && (val == nil || val == "" || val == 0) {
						continue
					}

					cols = append(cols, m.TargetColumn)
					placeholders = append(placeholders, "?")
					args = append(args, val)
				}
				query := fmt.Sprintf("INSERT INTO %s.%s (%s) VALUES (%s)",
					s.keyspace, table, strings.Join(cols, ", "), strings.Join(placeholders, ", "))
				batch.Query(query, args...)
			} else {
				query := fmt.Sprintf("INSERT INTO %s.%s (id, data) VALUES (?, ?)", s.keyspace, table)
				batch.Query(query, msg.ID(), msg.Payload())
			}
		}

		if err := s.session.ExecuteBatch(batch); err != nil {
			return fmt.Errorf("failed to execute batch for table %s: %w", table, err)
		}
	}

	return nil
}

func (s *CassandraSink) deleteMapped(ctx context.Context, batch *gocql.Batch, table string, msg hermod.Message) error {
	data := msg.Data()
	if data == nil {
		if len(msg.Before()) > 0 {
			_ = json.Unmarshal(msg.Before(), &data)
		} else if len(msg.Payload()) > 0 {
			_ = json.Unmarshal(msg.Payload(), &data)
		}
	}

	var pks []string
	var args []any

	for _, m := range s.mappings {
		if m.IsPrimaryKey {
			val := evaluator.GetMsgValByPath(msg, m.SourceField)
			pks = append(pks, fmt.Sprintf("%s = ?", m.TargetColumn))
			args = append(args, val)
		}
	}

	if len(pks) == 0 {
		query := fmt.Sprintf("DELETE FROM %s.%s WHERE id = ?", s.keyspace, table)
		batch.Query(query, msg.ID())
		return nil
	}

	if s.deleteStrategy == "soft_delete" && s.softDeleteColumn != "" {
		query := fmt.Sprintf("UPDATE %s.%s SET %s = ? WHERE %s",
			s.keyspace, table, s.softDeleteColumn, strings.Join(pks, " AND "))
		updateArgs := append([]any{s.softDeleteValue}, args...)
		batch.Query(query, updateArgs...)
		return nil
	}

	query := fmt.Sprintf("DELETE FROM %s.%s WHERE %s", s.keyspace, table, strings.Join(pks, " AND "))
	batch.Query(query, args...)
	return nil
}

func (s *CassandraSink) ensureTable(ctx context.Context, table string) error {
	if _, ok := s.verifiedTables.Load(table); ok {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.verifiedTables.Load(table); ok {
		return nil
	}

	// Check if table exists
	var count int
	checkQuery := "SELECT count(*) FROM system_schema.tables WHERE keyspace_name = ? AND table_name = ?"
	_ = s.session.Query(checkQuery, s.keyspace, table).WithContext(ctx).Scan(&count)
	exists := count > 0

	if exists {
		if s.autoTruncate {
			if err := s.session.Query(fmt.Sprintf("TRUNCATE %s.%s", s.keyspace, table)).WithContext(ctx).Exec(); err != nil {
				return fmt.Errorf("truncate table %s: %w", table, err)
			}
		}
		if s.autoSync && len(s.mappings) > 0 {
			if err := s.syncColumns(ctx, table); err != nil {
				return fmt.Errorf("sync columns %s: %w", table, err)
			}
		}
	} else {
		var query string
		if len(s.mappings) > 0 {
			var cols []string
			var pks []string
			for _, m := range s.mappings {
				dataType := m.DataType
				if dataType == "" {
					dataType = "text"
				}
				cols = append(cols, fmt.Sprintf("%s %s", m.TargetColumn, dataType))
				if m.IsPrimaryKey {
					pks = append(pks, m.TargetColumn)
				}
			}
			if len(pks) == 0 {
				pks = append(pks, s.mappings[0].TargetColumn)
			}
			query = fmt.Sprintf("CREATE TABLE %s.%s (%s, PRIMARY KEY (%s))",
				s.keyspace, table, strings.Join(cols, ", "), strings.Join(pks, ", "))
		} else {
			query = fmt.Sprintf("CREATE TABLE %s.%s (id text PRIMARY KEY, data text)", s.keyspace, table)
		}

		if err := s.session.Query(query).WithContext(ctx).Exec(); err != nil {
			return fmt.Errorf("failed to create table: %w", err)
		}
	}

	s.verifiedTables.Store(table, true)
	return nil
}

func (s *CassandraSink) syncColumns(ctx context.Context, table string) error {
	currentCols, err := s.DiscoverColumns(ctx, table)
	if err != nil {
		return err
	}

	colMap := make(map[string]bool)
	for _, col := range currentCols {
		colMap[col.Name] = true
	}

	for _, m := range s.mappings {
		if !colMap[m.TargetColumn] {
			dataType := m.DataType
			if dataType == "" {
				dataType = "text"
			}
			alterQuery := fmt.Sprintf("ALTER TABLE %s.%s ADD %s %s", s.keyspace, table, m.TargetColumn, dataType)
			if err := s.session.Query(alterQuery).WithContext(ctx).Exec(); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *CassandraSink) init() error {
	cluster := gocql.NewCluster(s.hosts...)
	cluster.Keyspace = s.keyspace
	session, err := cluster.CreateSession()
	if err != nil {
		return fmt.Errorf("failed to create cassandra session: %w", err)
	}
	s.session = session
	return nil
}

func (s *CassandraSink) Ping(ctx context.Context) error {
	if s.session == nil {
		if err := s.init(); err != nil {
			return err
		}
	}
	// Try a simple query to check if session is alive
	return s.session.Query("SELECT now() FROM system.local").WithContext(ctx).Exec()
}

func (s *CassandraSink) Close() error {
	if s.session != nil {
		s.session.Close()
	}
	return nil
}

func (s *CassandraSink) DiscoverDatabases(ctx context.Context) ([]string, error) {
	if s.session == nil {
		if err := s.init(); err != nil {
			return nil, err
		}
	}

	var keyspaces []string
	iter := s.session.Query("SELECT keyspace_name FROM system_schema.keyspaces").WithContext(ctx).Iter()
	var ks string
	for iter.Scan(&ks) {
		keyspaces = append(keyspaces, ks)
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return keyspaces, nil
}

func (s *CassandraSink) DiscoverTables(ctx context.Context) ([]string, error) {
	if s.session == nil {
		if err := s.init(); err != nil {
			return nil, err
		}
	}

	var tables []string
	iter := s.session.Query("SELECT table_name FROM system_schema.tables WHERE keyspace_name = ?", s.keyspace).WithContext(ctx).Iter()
	var table string
	for iter.Scan(&table) {
		tables = append(tables, table)
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return tables, nil
}

func (s *CassandraSink) DiscoverColumns(ctx context.Context, table string) ([]hermod.ColumnInfo, error) {
	if s.session == nil {
		if err := s.init(); err != nil {
			return nil, err
		}
	}

	var columns []hermod.ColumnInfo
	// Cassandra might have schema.table or just table
	targetKS := s.keyspace
	targetTable := table
	if strings.Contains(table, ".") {
		parts := strings.SplitN(table, ".", 2)
		targetKS = parts[0]
		targetTable = parts[1]
	}

	iter := s.session.Query("SELECT column_name, type, kind FROM system_schema.columns WHERE keyspace_name = ? AND table_name = ?", targetKS, targetTable).WithContext(ctx).Iter()
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
		return nil, err
	}
	return columns, nil
}
