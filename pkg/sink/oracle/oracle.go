package oracle

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	_ "github.com/sijms/go-ora/v2"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/evaluator"
	"github.com/user/hermod/pkg/sqlutil"
)

type OracleSink struct {
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

func NewOracleSink(connString string, tableName string, mappings []sqlutil.ColumnMapping, useExistingTable bool, deleteStrategy string, softDeleteColumn string, softDeleteValue string, operationMode string, autoTruncate bool, autoSync bool) *OracleSink {
	if operationMode == "" {
		operationMode = "auto"
	}
	return &OracleSink{
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

func (s *OracleSink) Write(ctx context.Context, msg hermod.Message) error {
	return s.WriteBatch(ctx, []hermod.Message{msg})
}

func (s *OracleSink) WriteBatch(ctx context.Context, msgs []hermod.Message) error {
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
		return fmt.Errorf("failed to begin oracle transaction: %w", err)
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
					MERGE INTO %s target
					USING (SELECT :1 AS id, :2 AS data FROM DUAL) source
					ON (target.id = source.id)
					WHEN MATCHED THEN UPDATE SET target.data = source.data
					WHEN NOT MATCHED THEN INSERT (id, data) VALUES (source.id, source.data)
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
				query := fmt.Sprintf("DELETE FROM %s WHERE id = :1", table)
				_, err = tx.ExecContext(ctx, query, msg.ID())
			}
		default:
			err = fmt.Errorf("unsupported operation: %s", op)
		}

		if err != nil {
			return fmt.Errorf("oracle write error on message %s: %w", msg.ID(), err)
		}
	}

	return tx.Commit()
}

func (s *OracleSink) upsertMapped(ctx context.Context, tx *sql.Tx, table string, msg hermod.Message) error {
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

		quoted, _ := sqlutil.QuoteIdent("oracle", m.TargetColumn)
		cols = append(cols, quoted)
		placeholder := fmt.Sprintf(":%d", len(args)+1)
		selectCols = append(selectCols, fmt.Sprintf("%s AS %s", placeholder, quoted))
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
		MERGE INTO %s target
		USING (SELECT %s FROM DUAL) source
		ON (target.%s = %s)
		WHEN MATCHED THEN UPDATE SET %s
		WHEN NOT MATCHED THEN INSERT (%s) VALUES (%s)
	`, table, strings.Join(selectCols, ", "), pkCol, pkSource, strings.Join(updateParts, ", "),
		targetCols, strings.Join(sourceCols, ", "))

	_, err := tx.ExecContext(ctx, query, args...)
	return err
}

func (s *OracleSink) insertMapped(ctx context.Context, tx *sql.Tx, table string, msg hermod.Message) error {
	var cols []string
	var placeholders []string
	var args []any

	for _, m := range s.mappings {
		val := evaluator.GetMsgValByPath(msg, m.SourceField)
		if m.IsIdentity && (val == nil || val == "" || val == 0) {
			continue
		}
		quoted, _ := sqlutil.QuoteIdent("oracle", m.TargetColumn)
		cols = append(cols, quoted)
		placeholders = append(placeholders, fmt.Sprintf(":%d", len(args)+1))
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

func (s *OracleSink) updateMapped(ctx context.Context, tx *sql.Tx, table string, msg hermod.Message) error {
	var updates []string
	var pks []string

	var allArgs []any
	for _, m := range s.mappings {
		if !m.IsPrimaryKey {
			val := evaluator.GetMsgValByPath(msg, m.SourceField)
			quoted, _ := sqlutil.QuoteIdent("oracle", m.TargetColumn)
			updates = append(updates, fmt.Sprintf("%s = :%d", quoted, len(allArgs)+1))
			allArgs = append(allArgs, val)
		}
	}
	for _, m := range s.mappings {
		if m.IsPrimaryKey {
			val := evaluator.GetMsgValByPath(msg, m.SourceField)
			quoted, _ := sqlutil.QuoteIdent("oracle", m.TargetColumn)
			pks = append(pks, fmt.Sprintf("%s = :%d", quoted, len(allArgs)+1))
			allArgs = append(allArgs, val)
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
	_, err := tx.ExecContext(ctx, query, allArgs...)
	return err
}

func (s *OracleSink) deleteMapped(ctx context.Context, tx *sql.Tx, table string, msg hermod.Message) error {
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
			qCol, _ := sqlutil.QuoteIdent("oracle", m.TargetColumn)
			pks = append(pks, fmt.Sprintf("%s = :%d", qCol, len(args)+1))
			args = append(args, val)
		}
	}

	if len(pks) == 0 {
		query := fmt.Sprintf("DELETE FROM %s WHERE id = :1", table)
		_, err := tx.ExecContext(ctx, query, msg.ID())
		return err
	}

	if s.deleteStrategy == "soft_delete" && s.softDeleteColumn != "" {
		qCol, _ := sqlutil.QuoteIdent("oracle", s.softDeleteColumn)
		query := fmt.Sprintf("UPDATE %s SET %s = :%d WHERE %s",
			table, qCol, len(args)+1, strings.Join(pks, " AND "))
		args = append(args, s.softDeleteValue)
		_, err := tx.ExecContext(ctx, query, args...)
		return err
	}

	query := fmt.Sprintf("DELETE FROM %s WHERE %s", table, strings.Join(pks, " AND "))
	_, err := tx.ExecContext(ctx, query, args...)
	return err
}

func (s *OracleSink) ensureTable(ctx context.Context, tx *sql.Tx, table string) error {
	if _, ok := s.verifiedTables.Load(table); ok {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.verifiedTables.Load(table); ok {
		return nil
	}

	if s.useExistingTable {
		s.verifiedTables.Store(table, true)
		return nil
	}

	quoted, _ := sqlutil.QuoteIdent("oracle", table)
	var query string
	if len(s.mappings) > 0 {
		var cols []string
		for _, m := range s.mappings {
			dataType := m.DataType
			if dataType == "" {
				dataType = "CLOB"
			}
			qCol, _ := sqlutil.QuoteIdent("oracle", m.TargetColumn)
			colDef := fmt.Sprintf("%s %s", qCol, dataType)
			if m.IsIdentity {
				if strings.Contains(strings.ToUpper(dataType), "INT") || strings.Contains(strings.ToUpper(dataType), "NUMBER") {
					colDef += " GENERATED ALWAYS AS IDENTITY"
				} else if strings.Contains(strings.ToUpper(dataType), "RAW") || strings.Contains(strings.ToUpper(dataType), "VARCHAR2") {
					colDef += " DEFAULT SYS_GUID()"
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
		query = fmt.Sprintf("CREATE TABLE %s (id VARCHAR2(255) PRIMARY KEY, data CLOB)", quoted)
	}

	// Oracle doesn't support IF NOT EXISTS for CREATE TABLE easily.
	// We check if it exists first.
	var count int
	checkQuery := "SELECT count(*) FROM user_tables WHERE table_name = :1"
	tableNameOnly := table
	if strings.Contains(table, ".") {
		parts := strings.Split(table, ".")
		tableNameOnly = parts[len(parts)-1]
	}
	_ = tx.QueryRowContext(ctx, checkQuery, strings.ToUpper(tableNameOnly)).Scan(&count)

	if count == 0 {
		if _, err := tx.ExecContext(ctx, query); err != nil {
			return err
		}
	}

	s.verifiedTables.Store(table, true)
	return nil
}

func (s *OracleSink) init(ctx context.Context) error {
	db, err := sql.Open("oracle", s.connString)
	if err != nil {
		return err
	}
	s.db = db
	return s.db.PingContext(ctx)
}

func (s *OracleSink) Ping(ctx context.Context) error {
	if s.db == nil {
		if err := s.init(ctx); err != nil {
			return err
		}
	}
	return s.db.PingContext(ctx)
}

func (s *OracleSink) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

func (s *OracleSink) DiscoverDatabases(ctx context.Context) ([]string, error) {
	// In Oracle, databases are more like instances or services.
	// Usually users are more interested in schemas (users).
	if s.db == nil {
		if err := s.init(ctx); err != nil {
			return nil, err
		}
	}
	rows, err := s.db.QueryContext(ctx, "SELECT username FROM all_users ORDER BY username")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		users = append(users, name)
	}
	return users, nil
}

func (s *OracleSink) DiscoverTables(ctx context.Context) ([]string, error) {
	if s.db == nil {
		if err := s.init(ctx); err != nil {
			return nil, err
		}
	}
	rows, err := s.db.QueryContext(ctx, "SELECT table_name FROM user_tables ORDER BY table_name")
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

func (s *OracleSink) DiscoverColumns(ctx context.Context, table string) ([]hermod.ColumnInfo, error) {
	if s.db == nil {
		if err := s.init(ctx); err != nil {
			return nil, err
		}
	}
	tableNameOnly := strings.ToUpper(table)
	if strings.Contains(table, ".") {
		parts := strings.Split(table, ".")
		tableNameOnly = strings.ToUpper(parts[len(parts)-1])
	}

	query := `
		SELECT column_name, data_type, nullable,
		(SELECT count(*) FROM all_cons_columns acc
		 JOIN all_constraints ac ON acc.constraint_name = ac.constraint_name
		 WHERE ac.table_name = utc.table_name AND ac.constraint_type = 'P' AND acc.column_name = utc.column_name) as is_pk,
		 NVL(identity_column, 'NO') as identity_column
		FROM user_tab_cols utc
		WHERE table_name = :1
		ORDER BY column_id
	`
	rows, err := s.db.QueryContext(ctx, query, tableNameOnly)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var columns []hermod.ColumnInfo
	for rows.Next() {
		var col hermod.ColumnInfo
		var nullable, identity string
		var isPK int
		if err := rows.Scan(&col.Name, &col.Type, &nullable, &isPK, &identity); err != nil {
			return nil, err
		}
		col.IsNullable = nullable == "Y"
		col.IsPK = isPK > 0
		col.IsIdentity = identity == "YES"
		columns = append(columns, col)
	}
	return columns, nil
}
