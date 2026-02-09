package mssql

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	_ "github.com/microsoft/go-mssqldb"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/evaluator"
	"github.com/user/hermod/pkg/sqlutil"
)

type MSSQLSink struct {
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

func NewMSSQLSink(connString string, tableName string, mappings []sqlutil.ColumnMapping, useExistingTable bool, deleteStrategy string, softDeleteColumn string, softDeleteValue string, operationMode string, autoTruncate bool, autoSync bool) *MSSQLSink {
	if operationMode == "" {
		operationMode = "auto"
	}
	return &MSSQLSink{
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

func (s *MSSQLSink) Write(ctx context.Context, msg hermod.Message) error {
	return s.WriteBatch(ctx, []hermod.Message{msg})
}

func (s *MSSQLSink) WriteBatch(ctx context.Context, msgs []hermod.Message) error {
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
		return fmt.Errorf("failed to begin mssql transaction: %w", err)
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
				table = fmt.Sprintf("%s.%s", msg.Schema(), table)
			}
		}

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
				// Basic upsert with MERGE
				query := fmt.Sprintf(`
					MERGE INTO %s AS target
					USING (SELECT ? AS id, ? AS data) AS source
					ON target.id = source.id
					WHEN MATCHED THEN UPDATE SET target.data = source.data
					WHEN NOT MATCHED THEN INSERT (id, data) VALUES (source.id, source.data);
				`, table)
				_, err = tx.ExecContext(ctx, query, msg.ID(), msg.Payload())
			}
		case hermod.OpDelete:
			if s.deleteStrategy == "ignore" {
				continue
			}
			if len(s.mappings) > 0 {
				err = s.deleteMapped(ctx, tx, table, msg)
			} else {
				query := fmt.Sprintf("DELETE FROM %s WHERE id = ?", table)
				_, err = tx.ExecContext(ctx, query, msg.ID())
			}
		default:
			err = fmt.Errorf("unsupported operation: %s", op)
		}

		if err != nil {
			return fmt.Errorf("mssql write error on message %s: %w", msg.ID(), err)
		}
	}

	return tx.Commit()
}

func (s *MSSQLSink) upsertMapped(ctx context.Context, tx *sql.Tx, table string, msg hermod.Message) error {
	data := msg.Data()
	if data == nil {
		_ = json.Unmarshal(msg.Payload(), &data)
	}

	var cols []string
	var selectCols []string
	var updateParts []string
	var args []any
	var pkCol string
	var pkSource string

	for _, m := range s.mappings {
		val := evaluator.GetMsgValByPath(msg, m.SourceField)

		if m.IsIdentity && (val == nil || val == "" || val == 0) {
			continue
		}

		quoted, _ := sqlutil.QuoteIdent("mssql", m.TargetColumn)
		cols = append(cols, quoted)
		selectCols = append(selectCols, fmt.Sprintf("? AS %s", quoted))
		args = append(args, val)

		if m.IsPrimaryKey {
			pkCol = quoted
			pkSource = "source." + quoted
		} else {
			updateParts = append(updateParts, fmt.Sprintf("target.%s = source.%s", quoted, quoted))
		}
	}

	if pkCol == "" {
		pkCol = cols[0]
		pkSource = "source." + cols[0]
	}

	targetCols := strings.Join(cols, ", ")
	sourceCols := make([]string, len(cols))
	for i, c := range cols {
		sourceCols[i] = "source." + c
	}

	query := fmt.Sprintf(`
		MERGE INTO %s AS target
		USING (SELECT %s) AS source
		ON target.%s = %s
		WHEN MATCHED THEN UPDATE SET %s
		WHEN NOT MATCHED THEN INSERT (%s) VALUES (%s);
	`, table, strings.Join(selectCols, ", "), pkCol, pkSource, strings.Join(updateParts, ", "),
		targetCols, strings.Join(sourceCols, ", "))

	_, err := tx.ExecContext(ctx, query, args...)
	return err
}

func (s *MSSQLSink) insertMapped(ctx context.Context, tx *sql.Tx, table string, msg hermod.Message) error {
	var cols []string
	var placeholders []string
	var args []any

	for _, m := range s.mappings {
		val := evaluator.GetMsgValByPath(msg, m.SourceField)
		if m.IsIdentity && (val == nil || val == "" || val == 0) {
			continue
		}
		quoted, _ := sqlutil.QuoteIdent("mssql", m.TargetColumn)
		cols = append(cols, quoted)
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

func (s *MSSQLSink) updateMapped(ctx context.Context, tx *sql.Tx, table string, msg hermod.Message) error {
	var updates []string
	var pks []string
	var args []any
	var pkArgs []any

	for _, m := range s.mappings {
		val := evaluator.GetMsgValByPath(msg, m.SourceField)
		quoted, _ := sqlutil.QuoteIdent("mssql", m.TargetColumn)
		if m.IsPrimaryKey {
			pks = append(pks, fmt.Sprintf("%s = ?", quoted))
			pkArgs = append(pkArgs, val)
		} else {
			updates = append(updates, fmt.Sprintf("%s = ?", quoted))
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

func (s *MSSQLSink) deleteMapped(ctx context.Context, tx *sql.Tx, table string, msg hermod.Message) error {
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
			qCol, _ := sqlutil.QuoteIdent("mssql", m.TargetColumn)
			pks = append(pks, fmt.Sprintf("%s = ?", qCol))
			args = append(args, val)
		}
	}

	if len(pks) == 0 {
		query := fmt.Sprintf("DELETE FROM %s WHERE id = ?", table)
		_, err := tx.ExecContext(ctx, query, msg.ID())
		return err
	}

	if s.deleteStrategy == "soft_delete" && s.softDeleteColumn != "" {
		qCol, _ := sqlutil.QuoteIdent("mssql", s.softDeleteColumn)
		query := fmt.Sprintf("UPDATE %s SET %s = ? WHERE %s",
			table, qCol, strings.Join(pks, " AND "))
		updateArgs := append([]any{s.softDeleteValue}, args...)
		_, err := tx.ExecContext(ctx, query, updateArgs...)
		return err
	}

	query := fmt.Sprintf("DELETE FROM %s WHERE %s", table, strings.Join(pks, " AND "))
	_, err := tx.ExecContext(ctx, query, args...)
	return err
}

func (s *MSSQLSink) ensureTable(ctx context.Context, tx *sql.Tx, table string) error {
	if _, ok := s.verifiedTables.Load(table); ok {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.verifiedTables.Load(table); ok {
		return nil
	}

	quoted, _ := sqlutil.QuoteIdent("mssql", table)

	// Check if table exists
	existsQuery := "SELECT COUNT(*) FROM sys.objects WHERE object_id = OBJECT_ID(?) AND type in (N'U')"
	var existsCount int
	_ = tx.QueryRowContext(ctx, existsQuery, table).Scan(&existsCount)
	exists := existsCount > 0

	if exists {
		if s.autoTruncate {
			if _, err := tx.ExecContext(ctx, fmt.Sprintf("TRUNCATE TABLE %s", quoted)); err != nil {
				return fmt.Errorf("failed to truncate table %s: %w", table, err)
			}
		}
		if s.autoSync && len(s.mappings) > 0 {
			if err := s.syncColumns(ctx, tx, table); err != nil {
				return fmt.Errorf("failed to sync columns for table %s: %w", table, err)
			}
		}
	} else {
		var query string
		if len(s.mappings) > 0 {
			var cols []string
			for _, m := range s.mappings {
				dataType := m.DataType
				if dataType == "" {
					dataType = "NVARCHAR(MAX)"
				}
				qCol, _ := sqlutil.QuoteIdent("mssql", m.TargetColumn)
				colDef := fmt.Sprintf("%s %s", qCol, dataType)
				if m.IsIdentity {
					if strings.Contains(strings.ToUpper(dataType), "INT") {
						colDef += " IDENTITY(1,1)"
					} else if strings.Contains(strings.ToUpper(dataType), "UNIQUEIDENTIFIER") {
						colDef += " DEFAULT NEWID()"
					}
				}
				if m.IsPrimaryKey {
					colDef += " PRIMARY KEY"
				} else if !m.IsNullable {
					colDef += " NOT NULL"
				}
				cols = append(cols, colDef)
			}
			query = fmt.Sprintf("CREATE TABLE %s (%s)", quoted, strings.Join(cols, ", "))
		} else {
			query = fmt.Sprintf("CREATE TABLE %s (id NVARCHAR(450) PRIMARY KEY, data NVARCHAR(MAX))", quoted)
		}

		if _, err := tx.ExecContext(ctx, query); err != nil {
			return err
		}
	}

	s.verifiedTables.Store(table, true)
	return nil
}

func (s *MSSQLSink) syncColumns(ctx context.Context, tx *sql.Tx, table string) error {
	query := `
		SELECT COLUMN_NAME, DATA_TYPE, IS_NULLABLE, 
		CASE WHEN EXISTS (
			SELECT 1 FROM INFORMATION_SCHEMA.KEY_COLUMN_USAGE kcu
			JOIN INFORMATION_SCHEMA.TABLE_CONSTRAINTS tc ON kcu.CONSTRAINT_NAME = tc.CONSTRAINT_NAME
			WHERE kcu.TABLE_NAME = c.TABLE_NAME AND tc.CONSTRAINT_TYPE = 'PRIMARY KEY' AND kcu.COLUMN_NAME = c.COLUMN_NAME
		) THEN 1 ELSE 0 END as IS_PK,
		ISNULL(COLUMNPROPERTY(OBJECT_ID(TABLE_SCHEMA + '.' + TABLE_NAME), COLUMN_NAME, 'IsIdentity'), 0) as IS_IDENTITY
		FROM INFORMATION_SCHEMA.COLUMNS c
		WHERE TABLE_NAME = ? OR TABLE_SCHEMA + '.' + TABLE_NAME = ?
	`
	rows, err := tx.QueryContext(ctx, query, table, table)
	if err != nil {
		return err
	}
	defer rows.Close()

	currentCols := make(map[string]hermod.ColumnInfo)
	for rows.Next() {
		var col hermod.ColumnInfo
		var nullable string
		var isPK, isIdentity int
		if err := rows.Scan(&col.Name, &col.Type, &nullable, &isPK, &isIdentity); err != nil {
			return err
		}
		col.IsNullable = nullable == "YES"
		col.IsPK = isPK == 1
		col.IsIdentity = isIdentity == 1
		currentCols[col.Name] = col
	}

	quotedTable, _ := sqlutil.QuoteIdent("mssql", table)

	// Add or Modify columns
	for _, m := range s.mappings {
		col, exists := currentCols[m.TargetColumn]
		dataType := m.DataType
		if dataType == "" {
			dataType = "NVARCHAR(MAX)"
		}

		if !exists {
			qCol, _ := sqlutil.QuoteIdent("mssql", m.TargetColumn)
			colDef := fmt.Sprintf("%s %s", qCol, dataType)
			if m.IsIdentity {
				if strings.Contains(strings.ToUpper(dataType), "INT") {
					colDef += " IDENTITY(1,1)"
				} else if strings.Contains(strings.ToUpper(dataType), "UNIQUEIDENTIFIER") {
					colDef += " DEFAULT NEWID()"
				}
			}
			if m.IsPrimaryKey {
				colDef += " PRIMARY KEY"
			} else if !m.IsNullable {
				colDef += " NOT NULL"
			}
			alterQuery := fmt.Sprintf("ALTER TABLE %s ADD %s", quotedTable, colDef)
			if _, err := tx.ExecContext(ctx, alterQuery); err != nil {
				return err
			}
		} else {
			// Basic type check
			if !strings.EqualFold(col.Type, dataType) && !strings.Contains(strings.ToLower(dataType), strings.ToLower(col.Type)) {
				qCol, _ := sqlutil.QuoteIdent("mssql", m.TargetColumn)
				alterQuery := fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s %s", quotedTable, qCol, dataType)
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
			qCol, _ := sqlutil.QuoteIdent("mssql", colName)
			alterQuery := fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s", quotedTable, qCol)
			_, _ = tx.ExecContext(ctx, alterQuery)
		}
	}

	return nil
}

func (s *MSSQLSink) init(ctx context.Context) error {
	db, err := sql.Open("sqlserver", s.connString)
	if err != nil {
		return err
	}
	s.db = db
	return s.db.PingContext(ctx)
}

func (s *MSSQLSink) Ping(ctx context.Context) error {
	if s.db == nil {
		if err := s.init(ctx); err != nil {
			return err
		}
	}
	return s.db.PingContext(ctx)
}

func (s *MSSQLSink) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

func (s *MSSQLSink) DiscoverDatabases(ctx context.Context) ([]string, error) {
	if s.db == nil {
		if err := s.init(ctx); err != nil {
			return nil, err
		}
	}
	rows, err := s.db.QueryContext(ctx, "SELECT name FROM sys.databases WHERE database_id > 4")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var dbs []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		dbs = append(dbs, name)
	}
	return dbs, nil
}

func (s *MSSQLSink) DiscoverTables(ctx context.Context) ([]string, error) {
	if s.db == nil {
		if err := s.init(ctx); err != nil {
			return nil, err
		}
	}
	rows, err := s.db.QueryContext(ctx, "SELECT TABLE_SCHEMA + '.' + TABLE_NAME FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_TYPE = 'BASE TABLE'")
	if err != nil {
		return nil, err
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

func (s *MSSQLSink) DiscoverColumns(ctx context.Context, table string) ([]hermod.ColumnInfo, error) {
	if s.db == nil {
		if err := s.init(ctx); err != nil {
			return nil, err
		}
	}
	// Simplified column discovery
	query := `
		SELECT COLUMN_NAME, DATA_TYPE, IS_NULLABLE, 
		CASE WHEN EXISTS (
			SELECT 1 FROM INFORMATION_SCHEMA.KEY_COLUMN_USAGE kcu
			JOIN INFORMATION_SCHEMA.TABLE_CONSTRAINTS tc ON kcu.CONSTRAINT_NAME = tc.CONSTRAINT_NAME
			WHERE kcu.TABLE_NAME = c.TABLE_NAME AND tc.CONSTRAINT_TYPE = 'PRIMARY KEY' AND kcu.COLUMN_NAME = c.COLUMN_NAME
		) THEN 1 ELSE 0 END as IS_PK,
		ISNULL(COLUMNPROPERTY(OBJECT_ID(TABLE_SCHEMA + '.' + TABLE_NAME), COLUMN_NAME, 'IsIdentity'), 0) as IS_IDENTITY
		FROM INFORMATION_SCHEMA.COLUMNS c
		WHERE TABLE_NAME = ? OR TABLE_SCHEMA + '.' + TABLE_NAME = ?
	`
	rows, err := s.db.QueryContext(ctx, query, table, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var columns []hermod.ColumnInfo
	for rows.Next() {
		var col hermod.ColumnInfo
		var nullable string
		var isPK, isIdentity int
		if err := rows.Scan(&col.Name, &col.Type, &nullable, &isPK, &isIdentity); err != nil {
			return nil, err
		}
		col.IsNullable = nullable == "YES"
		col.IsPK = isPK == 1
		col.IsIdentity = isIdentity == 1
		columns = append(columns, col)
	}
	return columns, nil
}
