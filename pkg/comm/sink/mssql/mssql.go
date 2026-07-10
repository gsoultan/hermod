package mssql

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"

	_ "github.com/microsoft/go-mssqldb"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/infra/evaluator"
	"github.com/user/hermod/pkg/infra/sqlutil"
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

	// group consecutive messages by table and effective operation
	type batch struct {
		table string
		op    hermod.Operation
		msgs  []hermod.Message
	}
	var batches []batch

	for _, msg := range msgs {
		if msg == nil {
			continue
		}

		table := s.resolveTableName(msg)
		op := s.resolveOperation(msg)

		if len(batches) > 0 && batches[len(batches)-1].table == table && batches[len(batches)-1].op == op {
			batches[len(batches)-1].msgs = append(batches[len(batches)-1].msgs, msg)
		} else {
			batches = append(batches, batch{table: table, op: op, msgs: []hermod.Message{msg}})
		}
	}

	for _, b := range batches {
		if err := s.ensureTable(ctx, tx, b.table); err != nil {
			return fmt.Errorf("ensure table %s: %w", b.table, err)
		}

		if err := s.executeBatch(ctx, tx, b.table, b.op, b.msgs); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *MSSQLSink) resolveTableName(msg hermod.Message) string {
	table := s.tableName
	if table == "" {
		table = msg.Table()
		if msg.Schema() != "" {
			table = fmt.Sprintf("%s.%s", msg.Schema(), table)
		}
	}
	return table
}

func (s *MSSQLSink) resolveOperation(msg hermod.Message) hermod.Operation {
	op := msg.Operation()
	if s.operationMode != "auto" && s.operationMode != "" {
		switch s.operationMode {
		case "insert":
			return hermod.OpCreate
		case "upsert":
			return hermod.OpUpdate
		case "update":
			return hermod.OpUpdate
		case "delete":
			return hermod.OpDelete
		}
	}
	if op == "" {
		return hermod.OpCreate
	}
	return op
}

func (s *MSSQLSink) executeBatch(ctx context.Context, tx *sql.Tx, table string, op hermod.Operation, msgs []hermod.Message) error {
	// Chunk the batch to avoid parameter count limits (MSSQL limit is 2100)
	paramsPerRow := len(s.mappings)
	if paramsPerRow == 0 {
		paramsPerRow = 2 // id and data
	}
	if op == hermod.OpDelete && len(s.mappings) > 0 {
		// count PKs
		paramsPerRow = 0
		for _, m := range s.mappings {
			if m.IsPrimaryKey {
				paramsPerRow++
			}
		}
	} else if op == hermod.OpDelete {
		paramsPerRow = 1 // id
	}

	chunkSize := 100
	if paramsPerRow > 0 {
		chunkSize = 2000 / paramsPerRow
	}
	if chunkSize < 1 {
		chunkSize = 1
	}

	for i := 0; i < len(msgs); i += chunkSize {
		end := i + chunkSize
		if end > len(msgs) {
			end = len(msgs)
		}
		chunk := msgs[i:end]

		var err error
		switch op {
		case hermod.OpCreate, hermod.OpSnapshot, hermod.OpUpdate:
			if len(s.mappings) > 0 {
				if op == hermod.OpCreate && s.operationMode == "insert" {
					err = s.insertMappedBatch(ctx, tx, table, chunk)
				} else if op == hermod.OpUpdate && s.operationMode == "update" {
					err = s.updateMappedBatch(ctx, tx, table, chunk)
				} else {
					err = s.upsertMappedBatch(ctx, tx, table, chunk)
				}
			} else {
				err = s.upsertBasicBatch(ctx, tx, table, chunk)
			}
		case hermod.OpDelete:
			if s.deleteStrategy == "ignore" {
				continue
			}
			if len(s.mappings) > 0 {
				err = s.deleteMappedBatch(ctx, tx, table, chunk)
			} else {
				err = s.deleteBasicBatch(ctx, tx, table, chunk)
			}
		default:
			err = fmt.Errorf("unsupported operation: %s", op)
		}

		if err != nil {
			return fmt.Errorf("mssql batch write error on table %s: %w", table, err)
		}
	}
	return nil
}

func (s *MSSQLSink) upsertMapped(ctx context.Context, tx *sql.Tx, table string, msg hermod.Message) error {
	return s.upsertMappedBatch(ctx, tx, table, []hermod.Message{msg})
}

func (s *MSSQLSink) insertMapped(ctx context.Context, tx *sql.Tx, table string, msg hermod.Message) error {
	return s.insertMappedBatch(ctx, tx, table, []hermod.Message{msg})
}

func (s *MSSQLSink) updateMapped(ctx context.Context, tx *sql.Tx, table string, msg hermod.Message) error {
	return s.updateMappedBatch(ctx, tx, table, []hermod.Message{msg})
}

func (s *MSSQLSink) deleteMapped(ctx context.Context, tx *sql.Tx, table string, msg hermod.Message) error {
	return s.deleteMappedBatch(ctx, tx, table, []hermod.Message{msg})
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
	existsQuery := "SELECT COUNT(*) FROM sys.objects WHERE object_id = OBJECT_ID(@p1) AND type in (N'U')"
	var existsCount int
	_ = tx.QueryRowContext(ctx, existsQuery, table).Scan(&existsCount)
	exists := existsCount > 0

	if exists {
		if s.autoTruncate {
			if _, err := tx.ExecContext(ctx, "TRUNCATE TABLE "+quoted); err != nil {
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
		WHERE TABLE_NAME = @p1 OR TABLE_SCHEMA + '.' + TABLE_NAME = @p2
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

func (s *MSSQLSink) upsertBasicBatch(ctx context.Context, tx *sql.Tx, table string, msgs []hermod.Message) error {
	if len(msgs) == 0 {
		return nil
	}
	var values []string
	var args []any
	for i, msg := range msgs {
		p1 := i*2 + 1
		p2 := i*2 + 2
		values = append(values, fmt.Sprintf("(@p%d, @p%d)", p1, p2))
		args = append(args, msg.ID(), msg.Payload())
	}

	query := fmt.Sprintf(`
		MERGE INTO %s AS target
		USING (VALUES %s) AS source (id, data)
		ON target.id = source.id
		WHEN MATCHED THEN UPDATE SET target.data = source.data
		WHEN NOT MATCHED THEN INSERT (id, data) VALUES (source.id, source.data);
	`, table, strings.Join(values, ", "))

	_, err := tx.ExecContext(ctx, query, args...)
	return err
}

func (s *MSSQLSink) deleteBasicBatch(ctx context.Context, tx *sql.Tx, table string, msgs []hermod.Message) error {
	if len(msgs) == 0 {
		return nil
	}
	var placeholders []string
	var args []any
	for i, msg := range msgs {
		placeholders = append(placeholders, fmt.Sprintf("@p%d", i+1))
		args = append(args, msg.ID())
	}

	query := fmt.Sprintf("DELETE FROM %s WHERE id IN (%s)", table, strings.Join(placeholders, ", "))
	_, err := tx.ExecContext(ctx, query, args...)
	return err
}

func (s *MSSQLSink) insertMappedBatch(ctx context.Context, tx *sql.Tx, table string, msgs []hermod.Message) error {
	if len(msgs) == 0 {
		return nil
	}

	var cols []string
	for _, m := range s.mappings {
		if m.SourceField == "" {
			continue
		}
		// In a batch, we assume columns are consistent. Identity columns without values are skipped.
		// We'll just check the first message to determine the column list.
		val := evaluator.GetMsgValByPath(msgs[0], m.SourceField)
		if m.IsIdentity && (val == nil || val == "" || val == 0) {
			continue
		}
		quoted, _ := sqlutil.QuoteIdent("mssql", m.TargetColumn)
		cols = append(cols, quoted)
	}

	if len(cols) == 0 {
		return nil
	}

	var values []string
	var args []any
	pIdx := 1
	for _, msg := range msgs {
		var rowPlaceholders []string
		for _, m := range s.mappings {
			if m.SourceField == "" {
				continue
			}
			val := evaluator.GetMsgValByPath(msg, m.SourceField)
			if m.IsIdentity && (val == nil || val == "" || val == 0) {
				continue
			}
			rowPlaceholders = append(rowPlaceholders, fmt.Sprintf("@p%d", pIdx))
			args = append(args, val)
			pIdx++
		}
		values = append(values, "("+strings.Join(rowPlaceholders, ", ")+")")
	}

	query := fmt.Sprintf("INSERT INTO %s (%s) VALUES %s",
		table, strings.Join(cols, ", "), strings.Join(values, ", "))
	_, err := tx.ExecContext(ctx, query, args...)
	return err
}

func (s *MSSQLSink) updateMappedBatch(ctx context.Context, tx *sql.Tx, table string, msgs []hermod.Message) error {
	// Update is harder to batch as a single statement unless using MERGE.
	// We'll use MERGE for updateMappedBatch as well to allow multi-row updates.
	return s.upsertMappedBatch(ctx, tx, table, msgs)
}

func (s *MSSQLSink) upsertMappedBatch(ctx context.Context, tx *sql.Tx, table string, msgs []hermod.Message) error {
	if len(msgs) == 0 {
		return nil
	}

	var cols []string
	var pkCols []string
	var updateParts []string
	for _, m := range s.mappings {
		if m.SourceField == "" {
			continue
		}
		val := evaluator.GetMsgValByPath(msgs[0], m.SourceField)
		if m.IsIdentity && (val == nil || val == "" || val == 0) {
			continue
		}
		quoted, _ := sqlutil.QuoteIdent("mssql", m.TargetColumn)
		cols = append(cols, quoted)
		if m.IsPrimaryKey {
			pkCols = append(pkCols, quoted)
		} else {
			updateParts = append(updateParts, fmt.Sprintf("target.%s = source.%s", quoted, quoted))
		}
	}

	if len(cols) == 0 {
		return nil
	}

	var values []string
	var args []any
	pIdx := 1
	for _, msg := range msgs {
		var rowPlaceholders []string
		for _, m := range s.mappings {
			if m.SourceField == "" {
				continue
			}
			val := evaluator.GetMsgValByPath(msg, m.SourceField)
			if m.IsIdentity && (val == nil || val == "" || val == 0) {
				continue
			}
			rowPlaceholders = append(rowPlaceholders, fmt.Sprintf("@p%d", pIdx))
			args = append(args, val)
			pIdx++
		}
		values = append(values, "("+strings.Join(rowPlaceholders, ", ")+")")
	}

	onPart := ""
	if len(pkCols) > 0 {
		var parts []string
		for _, pk := range pkCols {
			parts = append(parts, fmt.Sprintf("target.%s = source.%s", pk, pk))
		}
		onPart = strings.Join(parts, " AND ")
	} else {
		onPart = fmt.Sprintf("target.%s = source.%s", cols[0], cols[0])
	}

	sourceCols := make([]string, len(cols))
	for i, c := range cols {
		sourceCols[i] = "source." + c
	}

	query := fmt.Sprintf(`
		MERGE INTO %s AS target
		USING (VALUES %s) AS source (%s)
		ON %s
		WHEN MATCHED THEN UPDATE SET %s
		WHEN NOT MATCHED THEN INSERT (%s) VALUES (%s);
	`, table, strings.Join(values, ", "), strings.Join(cols, ", "),
		onPart, strings.Join(updateParts, ", "),
		strings.Join(cols, ", "), strings.Join(sourceCols, ", "))

	_, err := tx.ExecContext(ctx, query, args...)
	return err
}

func (s *MSSQLSink) deleteMappedBatch(ctx context.Context, tx *sql.Tx, table string, msgs []hermod.Message) error {
	if len(msgs) == 0 {
		return nil
	}

	var pkCols []string
	for _, m := range s.mappings {
		if m.IsPrimaryKey {
			quoted, _ := sqlutil.QuoteIdent("mssql", m.TargetColumn)
			pkCols = append(pkCols, quoted)
		}
	}

	if len(pkCols) == 0 {
		// Fallback to basic delete by ID if no mappings or no PKs defined
		return s.deleteBasicBatch(ctx, tx, table, msgs)
	}

	if s.deleteStrategy == "soft_delete" && s.softDeleteColumn != "" {
		qSoftCol, _ := sqlutil.QuoteIdent("mssql", s.softDeleteColumn)
		if len(pkCols) == 1 {
			pk := pkCols[0]
			var placeholders []string
			var args []any
			args = append(args, s.softDeleteValue)
			for i, msg := range msgs {
				var val any
				for _, m := range s.mappings {
					if m.IsPrimaryKey {
						val = evaluator.GetMsgValByPath(msg, m.SourceField)
						break
					}
				}
				placeholders = append(placeholders, fmt.Sprintf("@p%d", i+2))
				args = append(args, val)
			}
			query := fmt.Sprintf("UPDATE %s SET %s = @p1 WHERE %s IN (%s)", table, qSoftCol, pk, strings.Join(placeholders, ", "))
			_, err := tx.ExecContext(ctx, query, args...)
			return err
		}

		// Multi-column PK: use MERGE with UPDATE for soft delete
		var values []string
		var args []any
		args = append(args, s.softDeleteValue)
		pIdx := 2
		for _, msg := range msgs {
			var rowPlaceholders []string
			for _, m := range s.mappings {
				if m.IsPrimaryKey {
					val := evaluator.GetMsgValByPath(msg, m.SourceField)
					rowPlaceholders = append(rowPlaceholders, fmt.Sprintf("@p%d", pIdx))
					args = append(args, val)
					pIdx++
				}
			}
			values = append(values, "("+strings.Join(rowPlaceholders, ", ")+")")
		}

		var onParts []string
		for _, pk := range pkCols {
			onParts = append(onParts, fmt.Sprintf("target.%s = source.%s", pk, pk))
		}

		query := fmt.Sprintf(`
			MERGE INTO %s AS target
			USING (VALUES %s) AS source (%s)
			ON %s
			WHEN MATCHED THEN UPDATE SET target.%s = @p1;
		`, table, strings.Join(values, ", "), strings.Join(pkCols, ", "), strings.Join(onParts, " AND "), qSoftCol)

		_, err := tx.ExecContext(ctx, query, args...)
		return err
	}

	if len(pkCols) == 1 {
		// IN clause optimization
		pk := pkCols[0]
		var placeholders []string
		var args []any
		for i, msg := range msgs {
			var val any
			for _, m := range s.mappings {
				if m.IsPrimaryKey {
					val = evaluator.GetMsgValByPath(msg, m.SourceField)
					break
				}
			}
			placeholders = append(placeholders, fmt.Sprintf("@p%d", i+1))
			args = append(args, val)
		}
		query := fmt.Sprintf("DELETE FROM %s WHERE %s IN (%s)", table, pk, strings.Join(placeholders, ", "))
		_, err := tx.ExecContext(ctx, query, args...)
		return err
	}

	// Multi-column PK: use MERGE with DELETE
	var values []string
	var args []any
	pIdx := 1
	for _, msg := range msgs {
		var rowPlaceholders []string
		for _, m := range s.mappings {
			if m.IsPrimaryKey {
				val := evaluator.GetMsgValByPath(msg, m.SourceField)
				rowPlaceholders = append(rowPlaceholders, fmt.Sprintf("@p%d", pIdx))
				args = append(args, val)
				pIdx++
			}
		}
		values = append(values, "("+strings.Join(rowPlaceholders, ", ")+")")
	}

	var onParts []string
	for _, pk := range pkCols {
		onParts = append(onParts, fmt.Sprintf("target.%s = source.%s", pk, pk))
	}

	query := fmt.Sprintf(`
		MERGE INTO %s AS target
		USING (VALUES %s) AS source (%s)
		ON %s
		WHEN MATCHED THEN DELETE;
	`, table, strings.Join(values, ", "), strings.Join(pkCols, ", "), strings.Join(onParts, " AND "))

	_, err := tx.ExecContext(ctx, query, args...)
	return err
}

func (s *MSSQLSink) init(ctx context.Context) error {
	s.mu.Lock()
	if s.db != nil {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	db, err := sql.Open("sqlserver", s.connString)
	if err != nil {
		return err
	}

	// Set sensible connection pool defaults
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(15 * time.Minute)
	db.SetConnMaxIdleTime(5 * time.Minute)

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db != nil {
		db.Close()
		return nil
	}
	s.db = db
	return nil
}

func (s *MSSQLSink) Ping(ctx context.Context) error {
	s.mu.Lock()
	db := s.db
	s.mu.Unlock()
	if db == nil {
		if err := s.init(ctx); err != nil {
			return err
		}
		s.mu.Lock()
		db = s.db
		s.mu.Unlock()
	}
	return db.PingContext(ctx)
}

func (s *MSSQLSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db != nil {
		err := s.db.Close()
		s.db = nil
		return err
	}
	return nil
}

func (s *MSSQLSink) DiscoverDatabases(ctx context.Context) ([]string, error) {
	s.mu.Lock()
	db := s.db
	s.mu.Unlock()
	if db == nil {
		if err := s.init(ctx); err != nil {
			return nil, err
		}
		s.mu.Lock()
		db = s.db
		s.mu.Unlock()
	}
	rows, err := db.QueryContext(ctx, "SELECT name FROM sys.databases WHERE database_id > 4")
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
	s.mu.Lock()
	db := s.db
	s.mu.Unlock()
	if db == nil {
		if err := s.init(ctx); err != nil {
			return nil, err
		}
		s.mu.Lock()
		db = s.db
		s.mu.Unlock()
	}
	rows, err := db.QueryContext(ctx, "SELECT TABLE_SCHEMA + '.' + TABLE_NAME FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_TYPE = 'BASE TABLE'")
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
	s.mu.Lock()
	db := s.db
	s.mu.Unlock()
	if db == nil {
		if err := s.init(ctx); err != nil {
			return nil, err
		}
		s.mu.Lock()
		db = s.db
		s.mu.Unlock()
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
	rows, err := db.QueryContext(ctx, query, table, table)
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
