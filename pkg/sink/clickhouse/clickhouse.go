package clickhouse

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/evaluator"
	"github.com/user/hermod/pkg/sqlutil"
)

type ClickHouseSink struct {
	addr             string
	database         string
	conn             clickhouse.Conn
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

func NewClickHouseSink(addr string, database string, tableName string, mappings []sqlutil.ColumnMapping, useExistingTable bool, deleteStrategy string, softDeleteColumn string, softDeleteValue string, operationMode string, autoTruncate bool, autoSync bool) *ClickHouseSink {
	if operationMode == "" {
		operationMode = "auto"
	}
	return &ClickHouseSink{
		addr:             addr,
		database:         database,
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

func (s *ClickHouseSink) Write(ctx context.Context, msg hermod.Message) error {
	return s.WriteBatch(ctx, []hermod.Message{msg})
}

func (s *ClickHouseSink) WriteBatch(ctx context.Context, msgs []hermod.Message) error {
	// Filter nil messages
	filtered := make([]hermod.Message, 0, len(msgs))
	for _, m := range msgs {
		if m != nil {
			filtered = append(filtered, m)
		}
	}
	msgs = filtered

	if len(msgs) == 0 {
		return nil
	}
	if s.conn == nil {
		if err := s.init(ctx); err != nil {
			return err
		}
	}

	table := s.tableName
	if table == "" {
		table = msgs[0].Table()
	}

	if err := s.ensureTable(ctx, table); err != nil {
		return fmt.Errorf("ensure table %s: %w", table, err)
	}

	// Separate deletes from inserts
	var inserts []hermod.Message
	var deletes []hermod.Message

	for _, msg := range msgs {
		op := msg.Operation()
		if s.operationMode != "auto" && s.operationMode != "" {
			switch s.operationMode {
			case "insert", "upsert", "update":
				op = hermod.OpCreate
			case "delete":
				op = hermod.OpDelete
			}
		}

		if op == hermod.OpDelete {
			deletes = append(deletes, msg)
		} else {
			inserts = append(inserts, msg)
		}
	}

	if len(deletes) > 0 && s.deleteStrategy != "ignore" {
		for _, msg := range deletes {
			if len(s.mappings) > 0 {
				if err := s.deleteMapped(ctx, table, msg); err != nil {
					return err
				}
			} else {
				// Default hard delete
				query := fmt.Sprintf("ALTER TABLE %s.%s DELETE WHERE id = ?", s.database, table)
				if err := s.conn.Exec(ctx, query, msg.ID()); err != nil {
					return fmt.Errorf("clickhouse delete error: %w", err)
				}
			}
		}
	}

	if len(inserts) == 0 {
		return nil
	}

	var query string
	if len(s.mappings) > 0 {
		var insertCols []string
		for _, m := range s.mappings {
			if m.IsIdentity {
				continue
			}
			insertCols = append(insertCols, m.TargetColumn)
		}
		query = fmt.Sprintf("INSERT INTO %s.%s (%s)", s.database, table, strings.Join(insertCols, ", "))
	} else {
		query = fmt.Sprintf("INSERT INTO %s.%s (id, data)", s.database, table)
	}

	batch, err := s.conn.PrepareBatch(ctx, query)
	if err != nil {
		return err
	}

	for _, msg := range msgs {
		if len(s.mappings) > 0 {
			var args []any
			for _, m := range s.mappings {
				val := evaluator.GetMsgValByPath(msg, m.SourceField)
				if m.IsIdentity && (val == nil || val == "" || val == 0) {
					continue
				}
				args = append(args, val)
			}
			if err := batch.Append(args...); err != nil {
				return err
			}
		} else {
			if err := batch.Append(msg.ID(), string(msg.Payload())); err != nil {
				return err
			}
		}
	}

	return batch.Send()
}

func (s *ClickHouseSink) deleteMapped(ctx context.Context, table string, msg hermod.Message) error {
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
		query := fmt.Sprintf("ALTER TABLE %s.%s DELETE WHERE id = ?", s.database, table)
		return s.conn.Exec(ctx, query, msg.ID())
	}

	if s.deleteStrategy == "soft_delete" && s.softDeleteColumn != "" {
		query := fmt.Sprintf("ALTER TABLE %s.%s UPDATE %s = ? WHERE %s",
			s.database, table, s.softDeleteColumn, strings.Join(pks, " AND "))
		updateArgs := append([]any{s.softDeleteValue}, args...)
		return s.conn.Exec(ctx, query, updateArgs...)
	}

	query := fmt.Sprintf("ALTER TABLE %s.%s DELETE WHERE %s", s.database, table, strings.Join(pks, " AND "))
	return s.conn.Exec(ctx, query, args...)
}

func (s *ClickHouseSink) init(ctx context.Context) error {
	conn, err := clickhouse.Open(&clickhouse.Options{
		Addr: []string{s.addr},
		Auth: clickhouse.Auth{
			Database: s.database,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to connect to clickhouse: %w", err)
	}
	s.conn = conn
	return nil
}

func (s *ClickHouseSink) Ping(ctx context.Context) error {
	if s.conn == nil {
		if err := s.init(ctx); err != nil {
			return err
		}
	}
	return s.conn.Ping(ctx)
}

func (s *ClickHouseSink) Close() error {
	if s.conn != nil {
		return s.conn.Close()
	}
	return nil
}

func (s *ClickHouseSink) DiscoverDatabases(ctx context.Context) ([]string, error) {
	if s.conn == nil {
		if err := s.init(ctx); err != nil {
			return nil, err
		}
	}

	rows, err := s.conn.Query(ctx, "SHOW DATABASES")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var databases []string
	for rows.Next() {
		var db string
		if err := rows.Scan(&db); err != nil {
			return nil, err
		}
		databases = append(databases, db)
	}
	return databases, nil
}

func (s *ClickHouseSink) DiscoverTables(ctx context.Context) ([]string, error) {
	if s.conn == nil {
		if err := s.init(ctx); err != nil {
			return nil, err
		}
	}

	rows, err := s.conn.Query(ctx, "SHOW TABLES")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			return nil, err
		}
		tables = append(tables, table)
	}
	return tables, nil
}

func (s *ClickHouseSink) DiscoverColumns(ctx context.Context, table string) ([]hermod.ColumnInfo, error) {
	if s.conn == nil {
		if err := s.init(ctx); err != nil {
			return nil, err
		}
	}

	query := fmt.Sprintf("SELECT name, type, is_nullable = 'YES', is_in_primary_key, default_expression FROM system.columns WHERE table = '%s' AND database = '%s'", table, s.database)
	rows, err := s.conn.Query(ctx, query)
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

func (s *ClickHouseSink) ensureTable(ctx context.Context, table string) error {
	if _, ok := s.verifiedTables.Load(table); ok {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.verifiedTables.Load(table); ok {
		return nil
	}

	// Ensure database exists
	dbQuery := fmt.Sprintf("CREATE DATABASE IF NOT EXISTS %s", s.database)
	if err := s.conn.Exec(ctx, dbQuery); err != nil {
		// Ignore errors for database creation as user might not have permissions
	}

	// Check if table exists
	var count uint64
	checkQuery := "SELECT count() FROM system.tables WHERE database = ? AND name = ?"
	_ = s.conn.QueryRow(ctx, checkQuery, s.database, table).Scan(&count)
	exists := count > 0

	if exists {
		if s.autoTruncate {
			if err := s.conn.Exec(ctx, fmt.Sprintf("TRUNCATE TABLE %s.%s", s.database, table)); err != nil {
				return fmt.Errorf("truncate table %s: %w", table, err)
			}
		}
		if s.autoSync && len(s.mappings) > 0 {
			if err := s.syncColumns(ctx, table); err != nil {
				return fmt.Errorf("sync columns %s: %w", table, err)
			}
		}
	} else {
		// Ensure table exists
		var tableQuery string
		if len(s.mappings) > 0 {
			var cols []string
			var orderBy []string
			for _, m := range s.mappings {
				dataType := m.DataType
				if dataType == "" {
					dataType = "String"
				}
				if m.IsIdentity && strings.EqualFold(dataType, "UUID") {
					dataType = "UUID DEFAULT generateUUIDv4()"
				}
				if m.IsNullable && !strings.HasPrefix(strings.ToLower(dataType), "nullable") {
					dataType = fmt.Sprintf("Nullable(%s)", dataType)
				}
				cols = append(cols, fmt.Sprintf("%s %s", m.TargetColumn, dataType))
				if m.IsPrimaryKey {
					orderBy = append(orderBy, m.TargetColumn)
				}
			}
			if len(orderBy) == 0 {
				orderBy = append(orderBy, s.mappings[0].TargetColumn)
			}
			tableQuery = fmt.Sprintf("CREATE TABLE %s.%s (%s) ENGINE = MergeTree() ORDER BY (%s)",
				s.database, table, strings.Join(cols, ", "), strings.Join(orderBy, ", "))
		} else {
			tableQuery = fmt.Sprintf("CREATE TABLE %s.%s (id String, data String) ENGINE = MergeTree() ORDER BY id", s.database, table)
		}

		if err := s.conn.Exec(ctx, tableQuery); err != nil {
			return fmt.Errorf("create table: %w", err)
		}
	}

	s.verifiedTables.Store(table, true)
	return nil
}

func (s *ClickHouseSink) syncColumns(ctx context.Context, table string) error {
	currentCols, err := s.DiscoverColumns(ctx, table)
	if err != nil {
		return err
	}

	colMap := make(map[string]hermod.ColumnInfo)
	for _, col := range currentCols {
		colMap[col.Name] = col
	}

	for _, m := range s.mappings {
		_, exists := colMap[m.TargetColumn]
		if !exists {
			dataType := m.DataType
			if dataType == "" {
				dataType = "String"
			}
			if m.IsNullable && !strings.HasPrefix(strings.ToLower(dataType), "nullable") {
				dataType = fmt.Sprintf("Nullable(%s)", dataType)
			}
			alterQuery := fmt.Sprintf("ALTER TABLE %s.%s ADD COLUMN %s %s", s.database, table, m.TargetColumn, dataType)
			if err := s.conn.Exec(ctx, alterQuery); err != nil {
				return err
			}
		}
	}
	return nil
}
