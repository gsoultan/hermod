package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/evaluator"
	"github.com/user/hermod/pkg/message"
	"github.com/user/hermod/pkg/sqlutil"
	"log"
	"sync"
)

// PostgresSink implements the hermod.Sink interface for PostgreSQL.
type PostgresSink struct {
	connString       string
	pool             *pgxpool.Pool
	logger           hermod.Logger
	mu               sync.Mutex
	tx               pgx.Tx
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

func NewPostgresSink(connString string, tableName string, mappings []sqlutil.ColumnMapping, useExistingTable bool, deleteStrategy string, softDeleteColumn string, softDeleteValue string, operationMode string, autoTruncate bool, autoSync bool) *PostgresSink {
	if operationMode == "" {
		operationMode = "auto"
	}
	return &PostgresSink{
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

func (s *PostgresSink) SetLogger(logger hermod.Logger) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logger = logger
}

func (s *PostgresSink) log(level, msg string, keysAndValues ...any) {
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

func (s *PostgresSink) Write(ctx context.Context, msg hermod.Message) error {
	return s.WriteBatch(ctx, []hermod.Message{msg})
}

func (s *PostgresSink) WriteBatch(ctx context.Context, msgs []hermod.Message) error {
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
	if s.pool == nil {
		if err := s.init(ctx); err != nil {
			return err
		}
	}

	// Group by table and operation
	groups := make(map[string][]hermod.Message)
	for _, msg := range msgs {
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

		key := fmt.Sprintf("%s:%s", table, string(op))
		groups[key] = append(groups[key], msg)
	}

	var executor interface {
		Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
		Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
		QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	}

	if s.tx != nil {
		executor = s.tx
	} else {
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("failed to begin transaction: %w", err)
		}
		defer tx.Rollback(ctx)
		executor = tx
	}

	for key, group := range groups {
		parts := strings.SplitN(key, ":", 2)
		table := parts[0]

		// Ensure table exists
		if err := s.ensureTable(ctx, executor, table); err != nil {
			return fmt.Errorf("ensure table %s: %w", table, err)
		}

		op := hermod.Operation(parts[1])

		for _, msg := range group {
			var err error
			switch op {
			case hermod.OpCreate, hermod.OpSnapshot, hermod.OpUpdate:
				if len(s.mappings) > 0 {
					if s.operationMode == "insert" {
						err = s.insertMapped(ctx, executor, table, msg)
					} else if s.operationMode == "update" {
						err = s.updateMapped(ctx, executor, table, msg)
					} else {
						err = s.upsertMapped(ctx, executor, table, msg)
					}
				} else {
					query := fmt.Sprintf(commonQueries[QueryUpsert], table)
					_, err = executor.Exec(ctx, query, msg.ID(), msg.Payload())
				}
			case hermod.OpDelete:
				if s.deleteStrategy == "ignore" {
					continue
				}
				if len(s.mappings) > 0 {
					err = s.deleteMapped(ctx, executor, table, msg)
				} else {
					query := fmt.Sprintf(commonQueries[QueryDelete], table)
					_, err = executor.Exec(ctx, query, msg.ID())
				}
			default:
				err = fmt.Errorf("unsupported operation: %s", op)
			}

			if err != nil {
				return fmt.Errorf("batch write error on message %s: %w", msg.ID(), err)
			}
		}
	}

	if s.tx == nil {
		if tx, ok := executor.(pgx.Tx); ok {
			return tx.Commit(ctx)
		}
	}
	return nil
}

func (s *PostgresSink) init(ctx context.Context) error {
	pool, err := pgxpool.New(ctx, s.connString)
	if err != nil {
		return fmt.Errorf("failed to create postgres pool: %w", err)
	}
	s.pool = pool
	return s.pool.Ping(ctx)
}

func (s *PostgresSink) Begin(ctx context.Context) error {
	if err := s.init(ctx); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	s.tx = tx
	return nil
}

func (s *PostgresSink) Commit(ctx context.Context) error {
	if s.tx == nil {
		return fmt.Errorf("no active transaction")
	}
	err := s.tx.Commit(ctx)
	s.tx = nil
	return err
}

func (s *PostgresSink) Rollback(ctx context.Context) error {
	if s.tx == nil {
		return nil
	}
	err := s.tx.Rollback(ctx)
	s.tx = nil
	return err
}

func (s *PostgresSink) Prepare(ctx context.Context) (string, error) {
	if s.tx == nil {
		return "", fmt.Errorf("no active transaction")
	}
	txID := uuid.New().String()
	_, err := s.tx.Exec(ctx, fmt.Sprintf("PREPARE TRANSACTION '%s'", txID))
	if err != nil {
		return "", err
	}
	s.tx = nil
	return txID, nil
}

func (s *PostgresSink) CommitPrepared(ctx context.Context, txID string) error {
	if err := s.init(ctx); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, fmt.Sprintf("COMMIT PREPARED '%s'", txID))
	return err
}

func (s *PostgresSink) RollbackPrepared(ctx context.Context, txID string) error {
	if err := s.init(ctx); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, fmt.Sprintf("ROLLBACK PREPARED '%s'", txID))
	return err
}

func (s *PostgresSink) Ping(ctx context.Context) error {
	if s.pool == nil {
		if err := s.init(ctx); err != nil {
			return err
		}
	}
	return s.pool.Ping(ctx)
}

func (s *PostgresSink) Close() error {
	if s.pool != nil {
		s.pool.Close()
	}
	return nil
}

func (s *PostgresSink) DiscoverDatabases(ctx context.Context) ([]string, error) {
	if s.pool == nil {
		if err := s.init(ctx); err != nil {
			return nil, err
		}
	}

	rows, err := s.pool.Query(ctx, commonQueries[QueryListDatabases])
	if err != nil {
		return nil, fmt.Errorf("failed to query databases: %w", err)
	}
	defer rows.Close()

	var databases []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		databases = append(databases, name)
	}
	return databases, nil
}

func (s *PostgresSink) DiscoverTables(ctx context.Context) ([]string, error) {
	if s.pool == nil {
		if err := s.init(ctx); err != nil {
			return nil, err
		}
	}

	rows, err := s.pool.Query(ctx, commonQueries[QueryListTables])
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

func (s *PostgresSink) DiscoverColumns(ctx context.Context, table string) ([]hermod.ColumnInfo, error) {
	if s.pool == nil {
		if err := s.init(ctx); err != nil {
			return nil, err
		}
	}

	rows, err := s.pool.Query(ctx, commonQueries[QueryListColumns], table)
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

func (s *PostgresSink) Sample(ctx context.Context, table string) (hermod.Message, error) {
	msgs, err := s.Browse(ctx, table, 1)
	if err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		return nil, fmt.Errorf("no data found in table %s", table)
	}
	return msgs[0], nil
}

func (s *PostgresSink) Browse(ctx context.Context, table string, limit int) ([]hermod.Message, error) {
	if s.pool == nil {
		if err := s.init(ctx); err != nil {
			return nil, err
		}
	}

	quoted, err := sqlutil.QuoteIdent("pgx", table)
	if err != nil {
		return nil, fmt.Errorf("invalid table name: %w", err)
	}
	query := fmt.Sprintf(commonQueries[QueryBrowse], quoted, limit)
	rows, err := s.pool.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []hermod.Message
	for rows.Next() {
		fields := rows.FieldDescriptions()
		values, err := rows.Values()
		if err != nil {
			return nil, fmt.Errorf("failed to get values: %w", err)
		}

		record := make(map[string]any)
		for i, field := range fields {
			val := values[i]
			if b, ok := val.([]byte); ok {
				record[field.Name] = string(b)
			} else {
				record[field.Name] = val
			}
		}

		afterJSON, _ := json.Marshal(message.SanitizeMap(record))

		msg := message.AcquireMessage()
		msg.SetID(fmt.Sprintf("sample-%s-%d-%d", table, time.Now().Unix(), len(msgs)))
		msg.SetOperation(hermod.OpSnapshot)
		msg.SetTable(table)
		msg.SetAfter(afterJSON)
		msg.SetMetadata("source", "postgres_sink")
		msg.SetMetadata("sample", "true")
		msgs = append(msgs, msg)
	}

	return msgs, nil
}

func (s *PostgresSink) deleteMapped(ctx context.Context, executor interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}, table string, msg hermod.Message) error {
	data := msg.Data()
	if data == nil {
		// Try to parse from before payload if Data() is nil (it usually is for delete)
		if len(msg.Before()) > 0 {
			if err := json.Unmarshal(msg.Before(), &data); err != nil {
				return fmt.Errorf("failed to parse message before data: %w", err)
			}
		} else if len(msg.Payload()) > 0 {
			if err := json.Unmarshal(msg.Payload(), &data); err != nil {
				return fmt.Errorf("failed to parse message payload: %w", err)
			}
		}
	}

	var pks []string
	var args []any
	argIdx := 1

	for _, m := range s.mappings {
		if m.IsPrimaryKey {
			val := evaluator.GetMsgValByPath(msg, m.SourceField)
			pks = append(pks, fmt.Sprintf("%s = $%d", m.TargetColumn, argIdx))
			args = append(args, val)
			argIdx++
		}
	}

	if len(pks) == 0 {
		// Fallback to ID if no PK mapped
		query := fmt.Sprintf(commonQueries[QueryDelete], table)
		_, err := executor.Exec(ctx, query, msg.ID())
		return err
	}

	if s.deleteStrategy == "soft_delete" && s.softDeleteColumn != "" {
		query := fmt.Sprintf("UPDATE %s SET %s = $%d WHERE %s",
			table, s.softDeleteColumn, argIdx, strings.Join(pks, " AND "))
		args = append(args, s.softDeleteValue)
		_, err := executor.Exec(ctx, query, args...)
		return err
	}

	query := fmt.Sprintf("DELETE FROM %s WHERE %s", table, strings.Join(pks, " AND "))
	_, err := executor.Exec(ctx, query, args...)
	return err
}

func (s *PostgresSink) ensureTable(ctx context.Context, executor interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}, table string) error {
	if _, ok := s.verifiedTables.Load(table); ok {
		return nil
	}

	// Double check with mutex to avoid concurrent creation
	s.mu.Lock()
	defer s.mu.Unlock()

	// Re-check after acquiring lock
	if _, ok := s.verifiedTables.Load(table); ok {
		return nil
	}

	// Check if it's schema-qualified
	schema := ""
	tableNameOnly := table
	if strings.Contains(table, ".") {
		parts := strings.SplitN(table, ".", 2)
		schema = parts[0]
		tableNameOnly = parts[1]
		quotedSchema, err := sqlutil.QuoteIdent("pgx", schema)
		if err != nil {
			return fmt.Errorf("invalid schema name: %w", err)
		}
		schemaQuery := fmt.Sprintf(commonQueries[QueryCreateSchema], quotedSchema)
		if _, err := executor.Exec(ctx, schemaQuery); err != nil {
			// Ignore errors for schema creation
		}
	}

	quotedTable, err := sqlutil.QuoteIdent("pgx", table)
	if err != nil {
		return fmt.Errorf("invalid table name: %w", err)
	}

	// Check if table exists
	var exists bool
	checkQuery := "SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = $1"
	checkArgs := []any{tableNameOnly}
	if schema != "" {
		checkQuery += " AND table_schema = $2"
		checkArgs = append(checkArgs, schema)
	} else {
		checkQuery += " AND table_schema = current_schema()"
	}
	checkQuery += ")"

	err = executor.QueryRow(ctx, checkQuery, checkArgs...).Scan(&exists)

	if exists {
		if s.autoTruncate {
			if _, err := executor.Exec(ctx, fmt.Sprintf("TRUNCATE TABLE %s", quotedTable)); err != nil {
				return fmt.Errorf("failed to truncate table %s: %w", table, err)
			}
		}
		if s.autoSync && len(s.mappings) > 0 {
			if err := s.syncColumns(ctx, executor, table); err != nil {
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
					dataType = "TEXT" // Default
				}
				colDef := fmt.Sprintf("%s %s", m.TargetColumn, dataType)
				if m.IsIdentity {
					if strings.ToUpper(dataType) == "UUID" {
						colDef += " DEFAULT gen_random_uuid()"
					} else if strings.Contains(strings.ToUpper(dataType), "INT") {
						if strings.Contains(strings.ToUpper(dataType), "BIG") {
							dataType = "BIGSERIAL"
						} else {
							dataType = "SERIAL"
						}
						colDef = fmt.Sprintf("%s %s", m.TargetColumn, dataType)
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
			tableQuery = fmt.Sprintf(commonQueries[QueryCreateTable], quotedTable)
		}

		if _, err := executor.Exec(ctx, tableQuery); err != nil {
			return fmt.Errorf("create table: %w", err)
		}
	}

	s.verifiedTables.Store(table, true)
	return nil
}

func (s *PostgresSink) syncColumns(ctx context.Context, executor interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}, table string) error {
	rows, err := executor.Query(ctx, commonQueries[QueryListColumns], table)
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

	quotedTable, _ := sqlutil.QuoteIdent("pgx", table)

	// Add or Modify columns
	for _, m := range s.mappings {
		col, exists := currentCols[m.TargetColumn]
		dataType := m.DataType
		if dataType == "" {
			dataType = "TEXT"
		}

		if !exists {
			colDef := fmt.Sprintf("%s %s", m.TargetColumn, dataType)
			if m.IsIdentity {
				if strings.ToUpper(dataType) == "UUID" {
					colDef += " DEFAULT gen_random_uuid()"
				} else if strings.Contains(strings.ToUpper(dataType), "INT") {
					if strings.Contains(strings.ToUpper(dataType), "BIG") {
						dataType = "BIGSERIAL"
					} else {
						dataType = "SERIAL"
					}
					colDef = fmt.Sprintf("%s %s", m.TargetColumn, dataType)
				}
			}
			if m.IsPrimaryKey {
				colDef += " PRIMARY KEY"
			} else if !m.IsNullable {
				colDef += " NOT NULL"
			}
			alterQuery := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s", quotedTable, colDef)
			if _, err := executor.Exec(ctx, alterQuery); err != nil {
				return err
			}
		} else {
			// Basic type check
			if !strings.EqualFold(col.Type, dataType) && !strings.Contains(strings.ToLower(dataType), strings.ToLower(col.Type)) {
				alterQuery := fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s TYPE %s", quotedTable, m.TargetColumn, dataType)
				if _, err := executor.Exec(ctx, alterQuery); err != nil {
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
			alterQuery := fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s", quotedTable, colName)
			_, _ = executor.Exec(ctx, alterQuery)
		}
	}

	return nil
}

func (s *PostgresSink) upsertMapped(ctx context.Context, executor interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}, table string, msg hermod.Message) error {
	data := msg.Data()
	if data == nil {
		// Try to parse from payload if Data() is nil
		if err := json.Unmarshal(msg.Payload(), &data); err != nil {
			return fmt.Errorf("failed to parse message data: %w", err)
		}
	}

	var cols []string
	var placeholders []string
	var args []any
	var updates []string
	var pks []string

	argIdx := 1
	for _, m := range s.mappings {
		val := evaluator.GetMsgValByPath(msg, m.SourceField)

		if m.IsIdentity && (val == nil || val == "" || val == 0) {
			continue
		}

		cols = append(cols, m.TargetColumn)
		placeholders = append(placeholders, fmt.Sprintf("$%d", argIdx))
		args = append(args, val)
		argIdx++

		if m.IsPrimaryKey {
			pks = append(pks, m.TargetColumn)
		} else {
			updates = append(updates, fmt.Sprintf("%s = EXCLUDED.%s", m.TargetColumn, m.TargetColumn))
		}
	}

	if len(pks) == 0 {
		// Fallback to simple insert if no PK
		query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
			table, strings.Join(cols, ", "), strings.Join(placeholders, ", "))
		_, err := executor.Exec(ctx, query, args...)
		return err
	}

	query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) ON CONFLICT (%s) DO UPDATE SET %s",
		table,
		strings.Join(cols, ", "),
		strings.Join(placeholders, ", "),
		strings.Join(pks, ", "),
		strings.Join(updates, ", "))

	_, err := executor.Exec(ctx, query, args...)
	return err
}

func (s *PostgresSink) insertMapped(ctx context.Context, executor interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}, table string, msg hermod.Message) error {
	var cols []string
	var placeholders []string
	var args []any

	argIdx := 1
	for _, m := range s.mappings {
		val := evaluator.GetMsgValByPath(msg, m.SourceField)
		if m.IsIdentity && (val == nil || val == "" || val == 0) {
			continue
		}
		cols = append(cols, m.TargetColumn)
		placeholders = append(placeholders, fmt.Sprintf("$%d", argIdx))
		args = append(args, val)
		argIdx++
	}

	if len(cols) == 0 {
		return nil
	}

	query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
		table, strings.Join(cols, ", "), strings.Join(placeholders, ", "))
	_, err := executor.Exec(ctx, query, args...)
	return err
}

func (s *PostgresSink) updateMapped(ctx context.Context, executor interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}, table string, msg hermod.Message) error {
	var updates []string
	var pks []string

	argIdx := 1
	var allArgs []any
	for _, m := range s.mappings {
		if !m.IsPrimaryKey {
			val := evaluator.GetMsgValByPath(msg, m.SourceField)
			updates = append(updates, fmt.Sprintf("%s = $%d", m.TargetColumn, argIdx))
			allArgs = append(allArgs, val)
			argIdx++
		}
	}
	for _, m := range s.mappings {
		if m.IsPrimaryKey {
			val := evaluator.GetMsgValByPath(msg, m.SourceField)
			pks = append(pks, fmt.Sprintf("%s = $%d", m.TargetColumn, argIdx))
			allArgs = append(allArgs, val)
			argIdx++
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
	_, err := executor.Exec(ctx, query, allArgs...)
	return err
}
