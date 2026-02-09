package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/evaluator"
	"github.com/user/hermod/pkg/message"
	"github.com/user/hermod/pkg/sqlutil"
	_ "modernc.org/sqlite"
)

// SQLiteSink implements the hermod.Sink interface for SQLite.
type SQLiteSink struct {
	dbPath           string
	db               *sql.DB
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

func NewSQLiteSink(dbPath string, tableName string, mappings []sqlutil.ColumnMapping, useExistingTable bool, deleteStrategy string, softDeleteColumn string, softDeleteValue string, operationMode string, autoTruncate bool, autoSync bool) *SQLiteSink {
	if operationMode == "" {
		operationMode = "auto"
	}
	return &SQLiteSink{
		dbPath:           dbPath,
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

func (s *SQLiteSink) Write(ctx context.Context, msg hermod.Message) error {
	return s.WriteBatch(ctx, []hermod.Message{msg})
}

func (s *SQLiteSink) WriteBatch(ctx context.Context, msgs []hermod.Message) error {
	if len(msgs) == 0 {
		return nil
	}
	if s.db == nil {
		if err := s.init(ctx); err != nil {
			return err
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin sqlite transaction: %w", err)
	}
	defer tx.Rollback()

	for _, msg := range msgs {
		if msg == nil {
			continue
		}

		table := s.tableName
		if table == "" {
			table = msg.Table()
			if msg.Schema() != "" {
				table = fmt.Sprintf("%s_%s", msg.Schema(), table)
			}
		}

		// Ensure table exists
		if err := s.ensureTable(ctx, tx, table); err != nil {
			return fmt.Errorf("ensure table %s: %w", table, err)
		}

		op := msg.Operation()
		if s.operationMode != "auto" && s.operationMode != "" {
			switch s.operationMode {
			case "insert":
				op = hermod.OpCreate
			case "upsert":
				op = hermod.OpUpdate
			case "update":
				op = hermod.OpUpdate
			case "delete":
				op = hermod.OpDelete
			}
		}

		if op == "" {
			op = hermod.OpCreate
		}

		switch op {
		case hermod.OpCreate, hermod.OpSnapshot, hermod.OpUpdate:
			if len(s.mappings) > 0 {
				if s.operationMode == "insert" {
					err = s.insertMapped(ctx, tx, table, msg)
				} else if s.operationMode == "update" {
					err = s.updateMapped(ctx, tx, table, msg)
				} else {
					err = s.upsertMapped(ctx, tx, table, msg)
				}
			} else {
				query := fmt.Sprintf(commonQueries[QueryUpsert], table)
				_, err = tx.ExecContext(ctx, query, msg.ID(), msg.Payload())
			}
		case hermod.OpDelete:
			if s.deleteStrategy == "ignore" {
				continue
			}
			if len(s.mappings) > 0 {
				err = s.deleteMapped(ctx, tx, table, msg)
			} else {
				query := fmt.Sprintf(commonQueries[QueryDelete], table)
				_, err = tx.ExecContext(ctx, query, msg.ID())
			}
		default:
			err = fmt.Errorf("unsupported operation: %s", op)
		}

		if err != nil {
			return fmt.Errorf("sqlite write error on message %s: %w", msg.ID(), err)
		}
	}

	return tx.Commit()
}

func (s *SQLiteSink) deleteMapped(ctx context.Context, tx *sql.Tx, table string, msg hermod.Message) error {
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
			pks = append(pks, fmt.Sprintf("`%s` = ?", m.TargetColumn))
			args = append(args, val)
		}
	}

	if len(pks) == 0 {
		query := fmt.Sprintf(commonQueries[QueryDelete], table)
		_, err := tx.ExecContext(ctx, query, msg.ID())
		return err
	}

	if s.deleteStrategy == "soft_delete" && s.softDeleteColumn != "" {
		query := fmt.Sprintf("UPDATE %s SET `%s` = ? WHERE %s",
			table, s.softDeleteColumn, strings.Join(pks, " AND "))
		updateArgs := append([]any{s.softDeleteValue}, args...)
		_, err := tx.ExecContext(ctx, query, updateArgs...)
		return err
	}

	query := fmt.Sprintf("DELETE FROM %s WHERE %s", table, strings.Join(pks, " AND "))
	_, err := tx.ExecContext(ctx, query, args...)
	return err
}

func (s *SQLiteSink) ensureTable(ctx context.Context, tx *sql.Tx, table string) error {
	if _, ok := s.verifiedTables.Load(table); ok {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.verifiedTables.Load(table); ok {
		return nil
	}

	quotedTable, err := sqlutil.QuoteIdent("sqlite", table)
	if err != nil {
		return fmt.Errorf("invalid table name: %w", err)
	}

	// Check if table exists
	var exists bool
	checkQuery := "SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?"
	var count int
	if err := tx.QueryRowContext(ctx, checkQuery, table).Scan(&count); err == nil {
		exists = count > 0
	}

	if exists {
		if s.autoTruncate {
			if _, err := tx.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s", quotedTable)); err != nil {
				return fmt.Errorf("failed to truncate table %s: %w", table, err)
			}
		}
		if s.autoSync && len(s.mappings) > 0 {
			if err := s.syncColumns(ctx, tx, table); err != nil {
				return fmt.Errorf("failed to sync columns for table %s: %w", table, err)
			}
		}
	} else {
		var tableQuery string
		if len(s.mappings) > 0 {
			var cols []string
			for _, m := range s.mappings {
				dataType := m.DataType
				if dataType == "" {
					dataType = "TEXT"
				}
				colDef := fmt.Sprintf("`%s` %s", m.TargetColumn, dataType)
				if m.IsPrimaryKey {
					colDef += " PRIMARY KEY"
					if m.IsIdentity && strings.ToUpper(dataType) == "INTEGER" {
						colDef += " AUTOINCREMENT"
					}
				} else if !m.IsNullable {
					colDef += " NOT NULL"
				}
				cols = append(cols, colDef)
			}
			tableQuery = fmt.Sprintf("CREATE TABLE %s (%s)", quotedTable, strings.Join(cols, ", "))
		} else {
			tableQuery = fmt.Sprintf(commonQueries[QueryCreateTable], quotedTable)
		}

		if _, err := tx.ExecContext(ctx, tableQuery); err != nil {
			return fmt.Errorf("create table: %w", err)
		}
	}

	s.verifiedTables.Store(table, true)
	return nil
}

func (s *SQLiteSink) syncColumns(ctx context.Context, tx *sql.Tx, table string) error {
	rows, err := tx.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info('%s')", table))
	if err != nil {
		return err
	}
	defer rows.Close()

	currentCols := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, dtype string
		var notnull, pk int
		var dflt interface{}
		if err := rows.Scan(&cid, &name, &dtype, &notnull, &dflt, &pk); err != nil {
			return err
		}
		currentCols[name] = true
	}

	quotedTable, _ := sqlutil.QuoteIdent("sqlite", table)

	// Add missing columns
	for _, m := range s.mappings {
		if !currentCols[m.TargetColumn] {
			dataType := m.DataType
			if dataType == "" {
				dataType = "TEXT"
			}
			colDef := fmt.Sprintf("`%s` %s", m.TargetColumn, dataType)
			if !m.IsNullable {
				// SQLite has restrictions on adding NOT NULL columns without default
				// but let's try
				colDef += " NOT NULL DEFAULT ''"
			}
			alterQuery := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s", quotedTable, colDef)
			if _, err := tx.ExecContext(ctx, alterQuery); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *SQLiteSink) upsertMapped(ctx context.Context, tx *sql.Tx, table string, msg hermod.Message) error {
	data := msg.Data()
	if data == nil {
		if err := json.Unmarshal(msg.Payload(), &data); err != nil {
			return fmt.Errorf("failed to parse message data: %w", err)
		}
	}

	var cols []string
	var placeholders []string
	var args []any

	for _, m := range s.mappings {
		val := evaluator.GetMsgValByPath(msg, m.SourceField)

		if m.IsIdentity && (val == nil || val == "" || val == 0) {
			continue
		}

		cols = append(cols, fmt.Sprintf("`%s`", m.TargetColumn))
		placeholders = append(placeholders, "?")
		args = append(args, val)
	}

	query := fmt.Sprintf("INSERT OR REPLACE INTO %s (%s) VALUES (%s)",
		table, strings.Join(cols, ", "), strings.Join(placeholders, ", "))

	_, err := tx.ExecContext(ctx, query, args...)
	return err
}

func (s *SQLiteSink) insertMapped(ctx context.Context, tx *sql.Tx, table string, msg hermod.Message) error {
	var cols []string
	var placeholders []string
	var args []any

	for _, m := range s.mappings {
		val := evaluator.GetMsgValByPath(msg, m.SourceField)
		if m.IsIdentity && (val == nil || val == "" || val == 0) {
			continue
		}
		cols = append(cols, fmt.Sprintf("\"%s\"", m.TargetColumn))
		placeholders = append(placeholders, "?")
		args = append(args, val)
	}

	if len(cols) == 0 {
		return nil
	}

	query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
		table, strings.Join(cols, ", "), strings.Join(placeholders, ", "))
	_, err := tx.ExecContext(ctx, query, args...)
	return err
}

func (s *SQLiteSink) updateMapped(ctx context.Context, tx *sql.Tx, table string, msg hermod.Message) error {
	var updates []string
	var pks []string
	var args []any
	var pkArgs []any

	for _, m := range s.mappings {
		val := evaluator.GetMsgValByPath(msg, m.SourceField)
		if m.IsPrimaryKey {
			pks = append(pks, fmt.Sprintf("\"%s\" = ?", m.TargetColumn))
			pkArgs = append(pkArgs, val)
		} else {
			updates = append(updates, fmt.Sprintf("\"%s\" = ?", m.TargetColumn))
			args = append(args, val)
		}
	}

	if len(pks) == 0 {
		return fmt.Errorf("cannot update without primary key mappings")
	}
	if len(updates) == 0 {
		return nil
	}

	query := fmt.Sprintf("UPDATE %s SET %s WHERE %s",
		table, strings.Join(updates, ", "), strings.Join(pks, " AND "))
	args = append(args, pkArgs...)
	_, err := tx.ExecContext(ctx, query, args...)
	return err
}

func (s *SQLiteSink) init(ctx context.Context) error {
	dsn := s.dbPath
	if !strings.Contains(dsn, "?") {
		dsn += "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return fmt.Errorf("failed to open sqlite db: %w", err)
	}
	db.SetMaxOpenConns(1)
	s.db = db
	return s.db.PingContext(ctx)
}

func (s *SQLiteSink) Ping(ctx context.Context) error {
	if s.db == nil {
		if err := s.init(ctx); err != nil {
			return err
		}
	}
	return s.db.PingContext(ctx)
}

func (s *SQLiteSink) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

func (s *SQLiteSink) DiscoverDatabases(ctx context.Context) ([]string, error) {
	return []string{"main"}, nil
}

func (s *SQLiteSink) DiscoverTables(ctx context.Context) ([]string, error) {
	if s.db == nil {
		if err := s.init(ctx); err != nil {
			return nil, err
		}
	}

	rows, err := s.db.QueryContext(ctx, commonQueries[QueryListTables])
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

func (s *SQLiteSink) DiscoverColumns(ctx context.Context, table string) ([]hermod.ColumnInfo, error) {
	if s.db == nil {
		if err := s.init(ctx); err != nil {
			return nil, err
		}
	}

	quoted, err := sqlutil.QuoteIdent("sqlite", table)
	if err != nil {
		return nil, err
	}
	query := fmt.Sprintf(commonQueries[QueryListColumns], quoted)
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var columns []hermod.ColumnInfo
	for rows.Next() {
		var col hermod.ColumnInfo
		var cid int
		var notnull int
		var pk int
		var def *string
		if err := rows.Scan(&cid, &col.Name, &col.Type, &notnull, &def, &pk); err != nil {
			return nil, err
		}
		col.IsNullable = notnull == 0
		col.IsPK = pk > 0
		col.IsIdentity = col.IsPK && strings.ToUpper(col.Type) == "INTEGER"
		if def != nil {
			col.Default = *def
		}
		columns = append(columns, col)
	}
	return columns, nil
}

func (s *SQLiteSink) Sample(ctx context.Context, table string) (hermod.Message, error) {
	msgs, err := s.Browse(ctx, table, 1)
	if err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		return nil, fmt.Errorf("no data found in table %s", table)
	}
	return msgs[0], nil
}

func (s *SQLiteSink) Browse(ctx context.Context, table string, limit int) ([]hermod.Message, error) {
	if s.db == nil {
		if err := s.init(ctx); err != nil {
			return nil, err
		}
	}

	quoted, err := sqlutil.QuoteIdent("sqlite", table)
	if err != nil {
		return nil, fmt.Errorf("invalid table name: %w", err)
	}
	query := fmt.Sprintf(commonQueries[QueryBrowse], quoted, limit)
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []hermod.Message
	for rows.Next() {
		cols, err := rows.Columns()
		if err != nil {
			return nil, fmt.Errorf("failed to get columns: %w", err)
		}

		values := make([]interface{}, len(cols))
		valuePtrs := make([]interface{}, len(cols))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			return nil, err
		}

		record := make(map[string]interface{})
		for i, col := range cols {
			val := values[i]
			if b, ok := val.([]byte); ok {
				record[col] = string(b)
			} else {
				record[col] = val
			}
		}

		afterJSON, _ := json.Marshal(message.SanitizeMap(record))

		msg := message.AcquireMessage()
		msg.SetID(fmt.Sprintf("sample-%s-%d-%d", table, time.Now().Unix(), len(msgs)))
		msg.SetOperation(hermod.OpSnapshot)
		msg.SetTable(table)
		msg.SetAfter(afterJSON)
		msg.SetMetadata("source", "sqlite_sink")
		msg.SetMetadata("sample", "true")
		msgs = append(msgs, msg)
	}

	return msgs, nil
}
