package snowflake

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	_ "github.com/snowflakedb/gosnowflake"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/evaluator"
	"github.com/user/hermod/pkg/sqlutil"
)

// Sink implements the hermod.Sink interface for Snowflake.
type Sink struct {
	db               *sql.DB
	connString       string
	formatter        hermod.Formatter
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

func NewSink(connString string, formatter hermod.Formatter, tableName string, mappings []sqlutil.ColumnMapping, useExistingTable bool, deleteStrategy string, softDeleteColumn string, softDeleteValue string, operationMode string, autoTruncate bool, autoSync bool) *Sink {
	if operationMode == "" {
		operationMode = "auto"
	}
	return &Sink{
		connString:       connString,
		formatter:        formatter,
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

func (s *Sink) Write(ctx context.Context, msg hermod.Message) error {
	return s.WriteBatch(ctx, []hermod.Message{msg})
}

func (s *Sink) WriteBatch(ctx context.Context, msgs []hermod.Message) error {
	if len(msgs) == 0 {
		return nil
	}

	if s.db == nil {
		if err := s.init(); err != nil {
			return err
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Prepare statement cache per table for this transaction
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
				if err := s.deleteMapped(ctx, tx, table, msg); err != nil {
					return err
				}
			} else {
				query := fmt.Sprintf("DELETE FROM %s WHERE id = ?", table)
				_, err = tx.ExecContext(ctx, query, msg.ID())
				if err != nil {
					return fmt.Errorf("failed to execute delete for message %s: %w", msg.ID(), err)
				}
			}
			continue
		}

		if len(s.mappings) > 0 {
			if err := s.upsertMapped(ctx, tx, table, msg); err != nil {
				return err
			}
			continue
		}

		payload := msg.Payload()
		if s.formatter != nil {
			formatted, err := s.formatter.Format(msg)
			if err == nil {
				payload = formatted
			}
		}

		// Snowflake MERGE (UPSERT equivalent) — prepare per table
		key := "merge:" + table
		st := stmts[key]
		if st == nil {
			query := fmt.Sprintf(`
                MERGE INTO %s AS target
                USING (SELECT ? AS id, ? AS data) AS source
                ON target.id = source.id
                WHEN MATCHED THEN UPDATE SET target.data = source.data
                WHEN NOT MATCHED THEN INSERT (id, data) VALUES (source.id, source.data)
            `, table)
			st, err = tx.PrepareContext(ctx, query)
			if err != nil {
				return fmt.Errorf("prepare merge failed: %w", err)
			}
			stmts[key] = st
		}

		_, err = st.ExecContext(ctx, msg.ID(), payload)
		if err != nil {
			return fmt.Errorf("failed to execute merge for message %s: %w", msg.ID(), err)
		}
	}

	return tx.Commit()
}

func (s *Sink) deleteMapped(ctx context.Context, tx *sql.Tx, table string, msg hermod.Message) error {
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
		query := fmt.Sprintf("DELETE FROM %s WHERE id = ?", table)
		_, err := tx.ExecContext(ctx, query, msg.ID())
		return err
	}

	if s.deleteStrategy == "soft_delete" && s.softDeleteColumn != "" {
		query := fmt.Sprintf("UPDATE %s SET %s = ? WHERE %s",
			table, s.softDeleteColumn, strings.Join(pks, " AND "))
		updateArgs := append([]any{s.softDeleteValue}, args...)
		_, err := tx.ExecContext(ctx, query, updateArgs...)
		return err
	}

	query := fmt.Sprintf("DELETE FROM %s WHERE %s", table, strings.Join(pks, " AND "))
	_, err := tx.ExecContext(ctx, query, args...)
	return err
}

func (s *Sink) init() error {
	db, err := sql.Open("snowflake", s.connString)
	if err != nil {
		return err
	}
	// Conservative pool defaults
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(10)
	db.SetConnMaxIdleTime(60 * time.Second)
	s.db = db
	return s.db.Ping()
}

func (s *Sink) Ping(ctx context.Context) error {
	if s.db == nil {
		if err := s.init(); err != nil {
			return err
		}
	}
	return s.db.PingContext(ctx)
}

func (s *Sink) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

func (s *Sink) DiscoverColumns(ctx context.Context, table string) ([]hermod.ColumnInfo, error) {
	if s.db == nil {
		if err := s.init(); err != nil {
			return nil, err
		}
	}

	// In Snowflake, we use DESCRIBE TABLE
	query := fmt.Sprintf("DESCRIBE TABLE %s", table)
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var columns []hermod.ColumnInfo
	for rows.Next() {
		var col hermod.ColumnInfo
		var kind, isNull, def, isPK, isUnique, check, expr, comment, policy string
		// Snowflake DESC TABLE columns: name, type, kind, null?, default, primary key, unique key, check, expression, comment, policy name
		if err := rows.Scan(&col.Name, &col.Type, &kind, &isNull, &def, &isPK, &isUnique, &check, &expr, &comment, &policy); err != nil {
			return nil, err
		}
		col.IsNullable = isNull == "Y"
		col.IsPK = isPK == "Y"
		col.IsIdentity = strings.Contains(strings.ToUpper(def), "AUTOINCREMENT") || strings.Contains(strings.ToUpper(def), "IDENTITY")
		col.Default = def
		columns = append(columns, col)
	}
	return columns, nil
}
func (s *Sink) ensureTable(ctx context.Context, tx *sql.Tx, table string) error {
	if _, ok := s.verifiedTables.Load(table); ok {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.verifiedTables.Load(table); ok {
		return nil
	}

	// Ensure schema exists if provided
	schema := ""
	tableNameOnly := table
	if strings.Contains(table, ".") {
		parts := strings.SplitN(table, ".", 2)
		schema = parts[0]
		tableNameOnly = parts[1]
		quotedSchema, err := sqlutil.QuoteIdent("postgres", schema)
		if err != nil {
			return fmt.Errorf("invalid schema name: %w", err)
		}
		schemaQuery := fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", quotedSchema)
		if _, err := tx.ExecContext(ctx, schemaQuery); err != nil {
			// Ignore errors for schema creation
		}
	}

	quotedTable, err := sqlutil.QuoteIdent("postgres", table)
	if err != nil {
		return fmt.Errorf("invalid table name: %w", err)
	}

	// Check if table exists
	var exists bool
	checkQuery := "SELECT count(*) FROM information_schema.tables WHERE table_name = ? AND table_schema = ?"
	schemaToUse := strings.ToUpper(schema)
	if schemaToUse == "" {
		schemaToUse = "PUBLIC"
	}
	var count int
	if err := tx.QueryRowContext(ctx, checkQuery, strings.ToUpper(tableNameOnly), schemaToUse).Scan(&count); err == nil {
		exists = count > 0
	}

	if exists {
		if s.autoTruncate {
			if _, err := tx.ExecContext(ctx, fmt.Sprintf("TRUNCATE TABLE %s", quotedTable)); err != nil {
				return fmt.Errorf("truncate table %s: %w", table, err)
			}
		}
		if s.autoSync && len(s.mappings) > 0 {
			if err := s.syncColumns(ctx, tx, table); err != nil {
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
				colDef := fmt.Sprintf("%s %s", m.TargetColumn, dataType)
				if m.IsIdentity {
					if strings.Contains(strings.ToUpper(dataType), "INT") || strings.Contains(strings.ToUpper(dataType), "NUMBER") {
						colDef += " AUTOINCREMENT"
					} else {
						colDef += " DEFAULT UUID_STRING()"
					}
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
			tableQuery = fmt.Sprintf("CREATE TABLE %s (id TEXT PRIMARY KEY, data VARIANT)", quotedTable)
		}

		if _, err := tx.ExecContext(ctx, tableQuery); err != nil {
			return fmt.Errorf("create table: %w", err)
		}
	}

	s.verifiedTables.Store(table, true)
	return nil
}

func (s *Sink) syncColumns(ctx context.Context, tx *sql.Tx, table string) error {
	currentCols, err := s.DiscoverColumns(ctx, table)
	if err != nil {
		return err
	}

	colMap := make(map[string]bool)
	for _, col := range currentCols {
		colMap[strings.ToUpper(col.Name)] = true
	}

	quotedTable, _ := sqlutil.QuoteIdent("postgres", table)

	for _, m := range s.mappings {
		if !colMap[strings.ToUpper(m.TargetColumn)] {
			dataType := m.DataType
			if dataType == "" {
				dataType = "TEXT"
			}
			alterQuery := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", quotedTable, m.TargetColumn, dataType)
			if _, err := tx.ExecContext(ctx, alterQuery); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Sink) upsertMapped(ctx context.Context, tx *sql.Tx, table string, msg hermod.Message) error {
	data := msg.Data()
	if data == nil {
		if err := json.Unmarshal(msg.Payload(), &data); err != nil {
			return fmt.Errorf("failed to parse message data: %w", err)
		}
	}

	var cols []string
	var selectCols []string
	var args []any
	var updates []string
	var pks []string

	for _, m := range s.mappings {
		val := evaluator.GetMsgValByPath(msg, m.SourceField)

		if m.IsIdentity && (val == nil || val == "" || val == 0) {
			continue
		}

		cols = append(cols, m.TargetColumn)
		selectCols = append(selectCols, "? AS "+m.TargetColumn)
		args = append(args, val)

		if m.IsPrimaryKey {
			pks = append(pks, "target."+m.TargetColumn+" = source."+m.TargetColumn)
		} else {
			updates = append(updates, "target."+m.TargetColumn+" = source."+m.TargetColumn)
		}
	}

	if len(pks) == 0 {
		placeholders := make([]string, len(cols))
		for i := range placeholders {
			placeholders[i] = "?"
		}
		query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
			table, strings.Join(cols, ", "), strings.Join(placeholders, ", "))
		_, err := tx.ExecContext(ctx, query, args...)
		return err
	}

	targetCols := strings.Join(cols, ", ")
	sourceCols := strings.Join(cols, ", source.")
	query := fmt.Sprintf(`
        MERGE INTO %s AS target
        USING (SELECT %s) AS source
        ON %s
        WHEN MATCHED THEN UPDATE SET %s
        WHEN NOT MATCHED THEN INSERT (%s) VALUES (source.%s)
    `, table, strings.Join(selectCols, ", "), strings.Join(pks, " AND "),
		strings.Join(updates, ", "), targetCols, sourceCols)

	_, err := tx.ExecContext(ctx, query, args...)
	return err
}
