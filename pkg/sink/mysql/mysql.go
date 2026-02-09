package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/evaluator"
	"github.com/user/hermod/pkg/message"
	"github.com/user/hermod/pkg/sqlutil"
)

// MySQLSink implements the hermod.Sink interface for MySQL.
type MySQLSink struct {
	connString       string
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

func NewMySQLSink(connString string, tableName string, mappings []sqlutil.ColumnMapping, useExistingTable bool, deleteStrategy string, softDeleteColumn string, softDeleteValue string, operationMode string, autoTruncate bool, autoSync bool) *MySQLSink {
	if operationMode == "" {
		operationMode = "auto"
	}
	return &MySQLSink{
		connString:       connString,
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

func (s *MySQLSink) Write(ctx context.Context, msg hermod.Message) error {
	return s.WriteBatch(ctx, []hermod.Message{msg})
}

func (s *MySQLSink) WriteBatch(ctx context.Context, msgs []hermod.Message) error {
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
		return fmt.Errorf("failed to begin mysql transaction: %w", err)
	}
	defer tx.Rollback()

	// Prepare statement cache per table/op for this transaction to reduce parse overhead
	stmts := make(map[string]*sql.Stmt)
	defer func() {
		for _, st := range stmts {
			_ = st.Close()
		}
	}()

	for _, msg := range msgs {
		if msg == nil {
			continue
		}

		table := s.tableName
		if table == "" {
			table = msg.Table()
			if msg.Schema() != "" {
				table = fmt.Sprintf("%s.%s", msg.Schema(), table)
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
				key := "upsert:" + table
				st := stmts[key]
				if st == nil {
					query := fmt.Sprintf(commonQueries[QueryUpsert], table)
					st, err = tx.PrepareContext(ctx, query)
					if err != nil {
						return fmt.Errorf("prepare upsert failed: %w", err)
					}
					stmts[key] = st
				}
				_, err = st.ExecContext(ctx, msg.ID(), msg.Payload())
			}
		case hermod.OpDelete:
			if s.deleteStrategy == "ignore" {
				continue
			}
			if len(s.mappings) > 0 {
				err = s.deleteMapped(ctx, tx, table, msg)
			} else {
				key := "delete:" + table
				st := stmts[key]
				if st == nil {
					query := fmt.Sprintf(commonQueries[QueryDelete], table)
					st, err = tx.PrepareContext(ctx, query)
					if err != nil {
						return fmt.Errorf("prepare delete failed: %w", err)
					}
					stmts[key] = st
				}
				_, err = st.ExecContext(ctx, msg.ID())
			}
		default:
			err = fmt.Errorf("unsupported operation: %s", op)
		}

		if err != nil {
			return fmt.Errorf("mysql batch write error on message %s: %w", msg.ID(), err)
		}
	}

	return tx.Commit()
}

func (s *MySQLSink) init(ctx context.Context) error {
	db, err := sql.Open("mysql", s.connString)
	if err != nil {
		return fmt.Errorf("failed to connect to mysql: %w", err)
	}
	// Conservative pool defaults
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(10)
	db.SetConnMaxIdleTime(60 * time.Second)
	s.db = db
	return s.db.PingContext(ctx)
}

func (s *MySQLSink) Ping(ctx context.Context) error {
	if s.db == nil {
		if err := s.init(ctx); err != nil {
			return err
		}
	}
	return s.db.PingContext(ctx)
}

func (s *MySQLSink) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

func (s *MySQLSink) DiscoverDatabases(ctx context.Context) ([]string, error) {
	if s.db == nil {
		if err := s.init(ctx); err != nil {
			return nil, err
		}
	}

	rows, err := s.db.QueryContext(ctx, commonQueries[QueryShowDatabases])
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

func (s *MySQLSink) DiscoverTables(ctx context.Context) ([]string, error) {
	if s.db == nil {
		if err := s.init(ctx); err != nil {
			return nil, err
		}
	}

	rows, err := s.db.QueryContext(ctx, commonQueries[QueryShowTables])
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

func (s *MySQLSink) DiscoverColumns(ctx context.Context, table string) ([]hermod.ColumnInfo, error) {
	if s.db == nil {
		if err := s.init(ctx); err != nil {
			return nil, err
		}
	}

	rows, err := s.db.QueryContext(ctx, commonQueries[QueryListColumns], table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var columns []hermod.ColumnInfo
	for rows.Next() {
		var col hermod.ColumnInfo
		var def *string
		if err := rows.Scan(&col.Name, &col.Type, &col.IsNullable, &col.IsPK, &col.IsIdentity, &def); err != nil {
			return nil, err
		}
		if def != nil {
			col.Default = *def
		}
		columns = append(columns, col)
	}
	return columns, nil
}

func (s *MySQLSink) Sample(ctx context.Context, table string) (hermod.Message, error) {
	msgs, err := s.Browse(ctx, table, 1)
	if err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		return nil, fmt.Errorf("no data found in table %s", table)
	}
	return msgs[0], nil
}

func (s *MySQLSink) Browse(ctx context.Context, table string, limit int) ([]hermod.Message, error) {
	if s.db == nil {
		if err := s.init(ctx); err != nil {
			return nil, err
		}
	}

	quoted, err := sqlutil.QuoteIdent("mysql", table)
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
		msg.SetMetadata("source", "mysql_sink")
		msg.SetMetadata("sample", "true")
		msgs = append(msgs, msg)
	}

	return msgs, nil
}

func (s *MySQLSink) deleteMapped(ctx context.Context, tx *sql.Tx, table string, msg hermod.Message) error {
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

func (s *MySQLSink) ensureTable(ctx context.Context, tx *sql.Tx, table string) error {
	if _, ok := s.verifiedTables.Load(table); ok {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.verifiedTables.Load(table); ok {
		return nil
	}

	// In MySQL, "schema" is often a database name
	dbName := ""
	tableNameOnly := table
	if strings.Contains(table, ".") {
		parts := strings.SplitN(table, ".", 2)
		dbName = parts[0]
		tableNameOnly = parts[1]
		quotedDB, err := sqlutil.QuoteIdent("mysql", dbName)
		if err != nil {
			return fmt.Errorf("invalid database name: %w", err)
		}
		dbQuery := fmt.Sprintf(commonQueries[QueryCreateDatabase], quotedDB)
		if _, err := tx.ExecContext(ctx, dbQuery); err != nil {
			// Ignore errors for database creation as user might not have permissions
		}
	}

	quotedTable, err := sqlutil.QuoteIdent("mysql", table)
	if err != nil {
		return fmt.Errorf("invalid table name: %w", err)
	}

	// Check if table exists
	var exists bool
	checkQuery := "SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_NAME = ?"
	checkArgs := []any{tableNameOnly}
	if dbName != "" {
		checkQuery += " AND TABLE_SCHEMA = ?"
		checkArgs = append(checkArgs, dbName)
	} else {
		checkQuery += " AND TABLE_SCHEMA = DATABASE()"
	}

	var count int
	if err := tx.QueryRowContext(ctx, checkQuery, checkArgs...).Scan(&count); err == nil {
		exists = count > 0
	}

	if exists {
		if s.autoTruncate {
			if _, err := tx.ExecContext(ctx, fmt.Sprintf("TRUNCATE TABLE %s", quotedTable)); err != nil {
				return fmt.Errorf("truncate table %s: %w", table, err)
			}
		}
		if s.autoSync && len(s.mappings) > 0 {
			if err := s.syncColumns(ctx, tx, table, dbName, tableNameOnly); err != nil {
				return fmt.Errorf("sync columns %s: %w", table, err)
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
				if m.IsIdentity {
					colDef += " AUTO_INCREMENT"
				}
				if m.IsPrimaryKey {
					colDef += " PRIMARY KEY"
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

func (s *MySQLSink) syncColumns(ctx context.Context, tx *sql.Tx, table, dbName, tableNameOnly string) error {
	query := commonQueries[QueryListColumns]
	args := []any{tableNameOnly}
	if dbName != "" {
		query = strings.Replace(query, "TABLE_SCHEMA = DATABASE()", "TABLE_SCHEMA = ?", 1)
		args = append(args, dbName)
	}

	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	currentCols := make(map[string]hermod.ColumnInfo)
	for rows.Next() {
		var col hermod.ColumnInfo
		var def *string
		if err := rows.Scan(&col.Name, &col.Type, &col.IsNullable, &col.IsPK, &col.IsIdentity, &def); err != nil {
			return err
		}
		if def != nil {
			col.Default = *def
		}
		currentCols[col.Name] = col
	}

	quotedTable, _ := sqlutil.QuoteIdent("mysql", table)

	// Add or Modify columns
	for _, m := range s.mappings {
		col, exists := currentCols[m.TargetColumn]
		dataType := m.DataType
		if dataType == "" {
			dataType = "TEXT"
		}

		if !exists {
			colDef := fmt.Sprintf("`%s` %s", m.TargetColumn, dataType)
			if m.IsIdentity {
				colDef += " AUTO_INCREMENT"
			}
			if m.IsPrimaryKey {
				colDef += " PRIMARY KEY"
			} else if !m.IsNullable {
				colDef += " NOT NULL"
			}
			alterQuery := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s", quotedTable, colDef)
			if _, err := tx.ExecContext(ctx, alterQuery); err != nil {
				return err
			}
		} else {
			// Basic type check
			if !strings.EqualFold(col.Type, dataType) && !strings.Contains(strings.ToLower(dataType), strings.ToLower(col.Type)) {
				alterQuery := fmt.Sprintf("ALTER TABLE %s MODIFY COLUMN `%s` %s", quotedTable, m.TargetColumn, dataType)
				if _, err := tx.ExecContext(ctx, alterQuery); err != nil {
					return err
				}
			}
		}
	}

	// Drop columns not in mappings
	mappingCols := make(map[string]bool)
	for _, m := range s.mappings {
		mappingCols[m.TargetColumn] = true
	}
	for colName := range currentCols {
		if !mappingCols[colName] {
			alterQuery := fmt.Sprintf("ALTER TABLE %s DROP COLUMN `%s` ", quotedTable, colName)
			_, _ = tx.ExecContext(ctx, alterQuery)
		}
	}

	return nil
}

func (s *MySQLSink) upsertMapped(ctx context.Context, tx *sql.Tx, table string, msg hermod.Message) error {
	data := msg.Data()
	if data == nil {
		if err := json.Unmarshal(msg.Payload(), &data); err != nil {
			return fmt.Errorf("failed to parse message data: %w", err)
		}
	}

	var cols []string
	var placeholders []string
	var args []any
	var updates []string
	var pks []string

	for _, m := range s.mappings {
		val := evaluator.GetMsgValByPath(msg, m.SourceField)

		if m.IsIdentity && (val == nil || val == "" || val == 0) {
			continue
		}

		cols = append(cols, fmt.Sprintf("`%s`", m.TargetColumn))
		placeholders = append(placeholders, "?")
		args = append(args, val)

		if m.IsPrimaryKey {
			pks = append(pks, m.TargetColumn)
		} else {
			updates = append(updates, fmt.Sprintf("`%s` = VALUES(`%s`)", m.TargetColumn, m.TargetColumn))
		}
	}

	if len(pks) == 0 {
		query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
			table, strings.Join(cols, ", "), strings.Join(placeholders, ", "))
		_, err := tx.ExecContext(ctx, query, args...)
		return err
	}

	query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) ON DUPLICATE KEY UPDATE %s",
		table,
		strings.Join(cols, ", "),
		strings.Join(placeholders, ", "),
		strings.Join(updates, ", "))

	_, err := tx.ExecContext(ctx, query, args...)
	return err
}

func (s *MySQLSink) insertMapped(ctx context.Context, tx *sql.Tx, table string, msg hermod.Message) error {
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

	if len(cols) == 0 {
		return nil
	}

	query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
		table, strings.Join(cols, ", "), strings.Join(placeholders, ", "))
	_, err := tx.ExecContext(ctx, query, args...)
	return err
}

func (s *MySQLSink) updateMapped(ctx context.Context, tx *sql.Tx, table string, msg hermod.Message) error {
	var updates []string
	var pks []string
	var args []any
	var pkArgs []any

	for _, m := range s.mappings {
		val := evaluator.GetMsgValByPath(msg, m.SourceField)
		if m.IsPrimaryKey {
			pks = append(pks, fmt.Sprintf("`%s` = ?", m.TargetColumn))
			pkArgs = append(pkArgs, val)
		} else {
			updates = append(updates, fmt.Sprintf("`%s` = ?", m.TargetColumn))
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
