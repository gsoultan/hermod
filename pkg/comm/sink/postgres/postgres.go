package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/comm/message"
	"github.com/user/hermod/pkg/infra/evaluator"
	"github.com/user/hermod/pkg/infra/sqlutil"
)

// pgExecutor abstracts the subset of pgx behaviour shared by *pgxpool.Pool,
// *pgxpool.Tx and pgx.Tx, allowing the sink to operate transparently inside or
// outside an explicit transaction.
type pgExecutor interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

const pgDriver = "pgx"

// PostgresSink implements the hermod.Sink interface for PostgreSQL.
// All exported methods are safe for concurrent use.
type PostgresSink struct {
	connString       string
	pool             *pgxpool.Pool
	logger           hermod.Logger
	mu               sync.Mutex
	connMu           sync.Mutex
	tableLocks       sync.Map
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

func (s *PostgresSink) getLogger() hermod.Logger {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.logger
}

// quoteTable validates and quotes a (optionally schema-qualified) table identifier.
func quoteTable(table string) (string, error) {
	return sqlutil.QuoteIdent(pgDriver, table)
}

// quoteColumn validates and quotes a single column identifier.
func quoteColumn(column string) (string, error) {
	if err := sqlutil.ValidateIdent(column); err != nil {
		return "", fmt.Errorf("invalid column name %q: %w", column, err)
	}
	return sqlutil.QuoteIdent(pgDriver, column)
}

// isEmptyIdentity reports whether an identity column value should be omitted so
// the database can generate it. It is type-safe across the numeric kinds that
// convertValue and JSON decoding may produce.
func isEmptyIdentity(val any) bool {
	switch v := val.(type) {
	case nil:
		return true
	case string:
		return v == ""
	case int:
		return v == 0
	case int32:
		return v == 0
	case int64:
		return v == 0
	case uint:
		return v == 0
	case uint32:
		return v == 0
	case uint64:
		return v == 0
	case float32:
		return v == 0
	case float64:
		return v == 0
	default:
		return false
	}
}

func (s *PostgresSink) Write(ctx context.Context, msg hermod.Message) error {
	return s.WriteBatch(ctx, []hermod.Message{msg})
}

func (s *PostgresSink) WriteBatch(ctx context.Context, msgs []hermod.Message) error {
	msgs = filterNilMessages(msgs)
	if len(msgs) == 0 {
		return nil
	}
	if err := s.init(ctx); err != nil {
		return err
	}

	executor, localTx, err := s.beginExecution(ctx)
	if err != nil {
		return err
	}
	if localTx != nil {
		defer func() { _ = localTx.Rollback(ctx) }()
	}

	// Messages are applied in their original order to preserve change-data-capture
	// semantics (e.g. a delete followed by an insert for the same key).
	for _, msg := range msgs {
		if err := s.applyMessage(ctx, executor, msg); err != nil {
			return err
		}
	}

	if localTx != nil {
		return localTx.Commit(ctx)
	}
	return nil
}

// filterNilMessages removes nil entries while preserving order.
func filterNilMessages(msgs []hermod.Message) []hermod.Message {
	filtered := make([]hermod.Message, 0, len(msgs))
	for _, m := range msgs {
		if m != nil {
			filtered = append(filtered, m)
		}
	}
	return filtered
}

// beginExecution returns the executor to use. When an explicit transaction is
// active it is reused; otherwise a new transaction is started and returned so
// the caller can commit or roll it back.
func (s *PostgresSink) beginExecution(ctx context.Context) (pgExecutor, pgx.Tx, error) {
	if external := s.currentTx(); external != nil {
		return external, nil, nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	return tx, tx, nil
}

func (s *PostgresSink) applyMessage(ctx context.Context, executor pgExecutor, msg hermod.Message) error {
	table := s.resolveTable(msg)
	if err := s.ensureTable(ctx, executor, table); err != nil {
		return fmt.Errorf("ensure table %s: %w", table, err)
	}
	if err := s.applyOperation(ctx, executor, table, msg); err != nil {
		return fmt.Errorf("batch write error on message %s: %w", msg.ID(), err)
	}
	return nil
}

func (s *PostgresSink) resolveTable(msg hermod.Message) string {
	if s.tableName != "" {
		return s.tableName
	}
	table := msg.Table()
	if msg.Schema() != "" {
		return fmt.Sprintf("%s.%s", msg.Schema(), table)
	}
	return table
}

func (s *PostgresSink) resolveOperation(msg hermod.Message) hermod.Operation {
	op := msg.Operation()
	switch s.operationMode {
	case "insert":
		op = hermod.OpCreate
	case "upsert", "update":
		op = hermod.OpUpdate
	case "delete":
		op = hermod.OpDelete
	}
	if op == "" {
		op = hermod.OpCreate
	}
	return op
}

func (s *PostgresSink) applyOperation(ctx context.Context, executor pgExecutor, table string, msg hermod.Message) error {
	switch op := s.resolveOperation(msg); op {
	case hermod.OpCreate, hermod.OpSnapshot, hermod.OpUpdate:
		return s.applyUpsert(ctx, executor, table, msg)
	case hermod.OpDelete:
		if s.deleteStrategy == "ignore" {
			return nil
		}
		return s.applyDelete(ctx, executor, table, msg)
	default:
		return fmt.Errorf("unsupported operation: %s", op)
	}
}

func (s *PostgresSink) applyUpsert(ctx context.Context, executor pgExecutor, table string, msg hermod.Message) error {
	if len(s.mappings) > 0 {
		switch s.operationMode {
		case "insert":
			return s.insertMapped(ctx, executor, table, msg)
		case "update":
			return s.updateMapped(ctx, executor, table, msg)
		default:
			return s.upsertMapped(ctx, executor, table, msg)
		}
	}
	quoted, err := quoteTable(table)
	if err != nil {
		return fmt.Errorf("invalid table name: %w", err)
	}
	_, err = executor.Exec(ctx, fmt.Sprintf(commonQueries[QueryUpsert], quoted), msg.ID(), msg.Payload())
	return err
}

func (s *PostgresSink) applyDelete(ctx context.Context, executor pgExecutor, table string, msg hermod.Message) error {
	if len(s.mappings) > 0 {
		return s.deleteMapped(ctx, executor, table, msg)
	}
	quoted, err := quoteTable(table)
	if err != nil {
		return fmt.Errorf("invalid table name: %w", err)
	}
	_, err = executor.Exec(ctx, fmt.Sprintf(commonQueries[QueryDelete], quoted), msg.ID())
	return err
}

// init lazily creates the connection pool. It is safe for concurrent use and
// idempotent: only the first successful call establishes the pool.
func (s *PostgresSink) init(ctx context.Context) error {
	s.connMu.Lock()
	defer s.connMu.Unlock()
	if s.pool != nil {
		return nil
	}
	pool, err := pgxpool.New(ctx, s.connString)
	if err != nil {
		return fmt.Errorf("failed to create postgres pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return fmt.Errorf("failed to ping postgres: %w", err)
	}
	s.pool = pool
	return nil
}

func (s *PostgresSink) currentTx() pgx.Tx {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tx
}

func (s *PostgresSink) setTx(tx pgx.Tx) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tx = tx
}

func (s *PostgresSink) Begin(ctx context.Context) error {
	if err := s.init(ctx); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	s.setTx(tx)
	return nil
}

func (s *PostgresSink) Commit(ctx context.Context) error {
	tx := s.currentTx()
	if tx == nil {
		return errors.New("no active transaction")
	}
	err := tx.Commit(ctx)
	s.setTx(nil)
	return err
}

func (s *PostgresSink) Rollback(ctx context.Context) error {
	tx := s.currentTx()
	if tx == nil {
		return nil
	}
	err := tx.Rollback(ctx)
	s.setTx(nil)
	return err
}

func (s *PostgresSink) Prepare(ctx context.Context) (string, error) {
	tx := s.currentTx()
	if tx == nil {
		return "", errors.New("no active transaction")
	}
	txID := uuid.New().String()
	// PREPARE TRANSACTION only accepts a string literal, not a bind parameter.
	// txID is a server-generated UUID, so it is safe to interpolate.
	if _, err := tx.Exec(ctx, fmt.Sprintf("PREPARE TRANSACTION '%s'", txID)); err != nil {
		return "", err
	}
	// PREPARE TRANSACTION ends the server-side transaction but the pgx wrapper
	// still owns the pooled connection. Rolling back releases it back to the
	// pool (the redundant ROLLBACK is a harmless no-op on the server).
	_ = tx.Rollback(ctx)
	s.setTx(nil)
	return txID, nil
}

func (s *PostgresSink) CommitPrepared(ctx context.Context, txID string) error {
	if err := validateTxID(txID); err != nil {
		return err
	}
	if err := s.init(ctx); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, fmt.Sprintf("COMMIT PREPARED '%s'", txID))
	return err
}

func (s *PostgresSink) RollbackPrepared(ctx context.Context, txID string) error {
	if err := validateTxID(txID); err != nil {
		return err
	}
	if err := s.init(ctx); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, fmt.Sprintf("ROLLBACK PREPARED '%s'", txID))
	return err
}

// validateTxID ensures a prepared-transaction identifier is a well-formed UUID
// before it is interpolated into a COMMIT/ROLLBACK PREPARED statement, which
// does not accept bind parameters.
func validateTxID(txID string) error {
	if _, err := uuid.Parse(txID); err != nil {
		return fmt.Errorf("invalid prepared transaction id: %w", err)
	}
	return nil
}

func (s *PostgresSink) Ping(ctx context.Context) error {
	if err := s.init(ctx); err != nil {
		return err
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
	if err := s.init(ctx); err != nil {
		return nil, err
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
	if err := s.init(ctx); err != nil {
		return nil, err
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
	if err := s.init(ctx); err != nil {
		return nil, err
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
	if err := s.init(ctx); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 1
	}

	quoted, err := quoteTable(table)
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

func (s *PostgresSink) deleteMapped(ctx context.Context, executor pgExecutor, table string, msg hermod.Message) error {
	quoted, err := quoteTable(table)
	if err != nil {
		return fmt.Errorf("invalid table name: %w", err)
	}

	pks, args, err := s.primaryKeyPredicates(msg, 1)
	if err != nil {
		return err
	}

	if len(pks) == 0 {
		// Fallback to the synthetic id column when no primary key is mapped.
		_, err := executor.Exec(ctx, fmt.Sprintf(commonQueries[QueryDelete], quoted), msg.ID())
		return err
	}

	if s.deleteStrategy == "soft_delete" && s.softDeleteColumn != "" {
		col, err := quoteColumn(s.softDeleteColumn)
		if err != nil {
			return err
		}
		query := fmt.Sprintf("UPDATE %s SET %s = $%d WHERE %s",
			quoted, col, len(args)+1, strings.Join(pks, " AND "))
		args = append(args, s.softDeleteValue)
		_, err = executor.Exec(ctx, query, args...)
		return err
	}

	query := fmt.Sprintf("DELETE FROM %s WHERE %s", quoted, strings.Join(pks, " AND "))
	_, err = executor.Exec(ctx, query, args...)
	return err
}

// primaryKeyPredicates builds "col = $N" predicates for every primary-key
// mapping, returning the quoted predicates and the converted bind values.
func (s *PostgresSink) primaryKeyPredicates(msg hermod.Message, startIdx int) ([]string, []any, error) {
	var pks []string
	var args []any
	argIdx := startIdx
	for _, m := range s.mappings {
		if !m.IsPrimaryKey {
			continue
		}
		col, err := quoteColumn(m.TargetColumn)
		if err != nil {
			return nil, nil, err
		}
		val := s.convertValue(evaluator.GetMsgValByPath(msg, m.SourceField), m.DataType)
		pks = append(pks, fmt.Sprintf("%s = $%d", col, argIdx))
		args = append(args, val)
		argIdx++
	}
	return pks, args, nil
}

func (s *PostgresSink) ensureTable(ctx context.Context, executor pgExecutor, table string) error {
	if _, ok := s.verifiedTables.Load(table); ok {
		return nil
	}

	// A per-table lock prevents concurrent DDL for the same table while still
	// allowing writes to unrelated tables to proceed in parallel.
	unlock := s.lockTable(table)
	defer unlock()

	if _, ok := s.verifiedTables.Load(table); ok {
		return nil
	}

	schema, tableNameOnly := splitSchemaTable(table)
	if schema != "" {
		s.ensureSchema(ctx, executor, schema)
	}

	quotedTable, err := quoteTable(table)
	if err != nil {
		return fmt.Errorf("invalid table name: %w", err)
	}

	exists, err := tableExists(ctx, executor, schema, tableNameOnly)
	if err != nil {
		return fmt.Errorf("failed to check table existence for %s: %w", table, err)
	}

	if exists {
		if err := s.reconcileExistingTable(ctx, executor, table, quotedTable); err != nil {
			return err
		}
	} else if err := s.createTable(ctx, executor, quotedTable); err != nil {
		return err
	}

	s.verifiedTables.Store(table, true)
	return nil
}

// lockTable returns an unlock function for a per-table mutex.
func (s *PostgresSink) lockTable(table string) func() {
	v, _ := s.tableLocks.LoadOrStore(table, &sync.Mutex{})
	m, _ := v.(*sync.Mutex)
	m.Lock()
	return m.Unlock
}

func splitSchemaTable(table string) (schema, name string) {
	if idx := strings.Index(table, "."); idx >= 0 {
		return table[:idx], table[idx+1:]
	}
	return "", table
}

// ensureSchema best-effort creates the schema. Failures (e.g. insufficient
// privileges) are logged but not fatal, mirroring PostgreSQL search-path semantics.
func (s *PostgresSink) ensureSchema(ctx context.Context, executor pgExecutor, schema string) {
	quotedSchema, err := quoteColumn(schema)
	if err != nil {
		return
	}
	if _, err := executor.Exec(ctx, fmt.Sprintf(commonQueries[QueryCreateSchema], quotedSchema)); err != nil {
		if l := s.getLogger(); l != nil {
			l.Debug("postgres sink: schema creation skipped", "schema", schema, "error", err.Error())
		}
	}
}

func tableExists(ctx context.Context, executor pgExecutor, schema, name string) (bool, error) {
	query := commonQueries[QueryTableExists]
	args := []any{name}
	if schema != "" {
		query += " AND table_schema = $2"
		args = append(args, schema)
	} else {
		query += " AND table_schema = current_schema()"
	}
	query += ")"

	var exists bool
	if err := executor.QueryRow(ctx, query, args...).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

func (s *PostgresSink) reconcileExistingTable(ctx context.Context, executor pgExecutor, table, quotedTable string) error {
	if s.autoTruncate {
		if _, err := executor.Exec(ctx, "TRUNCATE TABLE "+quotedTable); err != nil {
			return fmt.Errorf("failed to truncate table %s: %w", table, err)
		}
	}
	if s.autoSync && len(s.mappings) > 0 {
		if err := s.syncColumns(ctx, executor, table); err != nil {
			return fmt.Errorf("failed to sync columns for table %s: %w", table, err)
		}
	}
	return nil
}

func (s *PostgresSink) createTable(ctx context.Context, executor pgExecutor, quotedTable string) error {
	var query string
	if len(s.mappings) > 0 {
		cols := make([]string, 0, len(s.mappings))
		for _, m := range s.mappings {
			colDef, err := buildColumnDefinition(m)
			if err != nil {
				return err
			}
			cols = append(cols, colDef)
		}
		query = fmt.Sprintf("CREATE TABLE %s (%s)", quotedTable, strings.Join(cols, ", "))
	} else {
		query = fmt.Sprintf(commonQueries[QueryCreateTable], quotedTable)
	}
	if _, err := executor.Exec(ctx, query); err != nil {
		return fmt.Errorf("create table: %w", err)
	}
	return nil
}

// buildColumnDefinition renders a safe "col TYPE [constraints]" fragment for a mapping.
func buildColumnDefinition(m sqlutil.ColumnMapping) (string, error) {
	col, err := quoteColumn(m.TargetColumn)
	if err != nil {
		return "", err
	}
	dataType, err := resolveDataType(m)
	if err != nil {
		return "", err
	}
	def := col + " " + dataType
	if m.IsIdentity && strings.EqualFold(dataType, "UUID") {
		def += " DEFAULT gen_random_uuid()"
	}
	switch {
	case m.IsPrimaryKey:
		def += " PRIMARY KEY"
	case !m.IsNullable:
		def += " NOT NULL"
	}
	return def, nil
}

// resolveDataType applies defaults and identity-to-serial promotion, then
// validates the resulting type so it can be safely interpolated into DDL.
func resolveDataType(m sqlutil.ColumnMapping) (string, error) {
	dataType := m.DataType
	if dataType == "" {
		dataType = "TEXT"
	}
	if m.IsIdentity && strings.Contains(strings.ToUpper(dataType), "INT") {
		if strings.Contains(strings.ToUpper(dataType), "BIG") {
			dataType = "BIGSERIAL"
		} else {
			dataType = "SERIAL"
		}
	}
	if err := validateDataType(dataType); err != nil {
		return "", err
	}
	return dataType, nil
}

var dataTypeRe = regexp.MustCompile(`^[A-Za-z0-9_ ,()]+$`)

func validateDataType(dataType string) error {
	if !dataTypeRe.MatchString(dataType) {
		return fmt.Errorf("invalid column data type: %q", dataType)
	}
	return nil
}

func (s *PostgresSink) syncColumns(ctx context.Context, executor pgExecutor, table string) error {
	current, err := loadColumns(ctx, executor, table)
	if err != nil {
		return err
	}

	quotedTable, err := quoteTable(table)
	if err != nil {
		return err
	}

	if err := s.addOrAlterColumns(ctx, executor, quotedTable, current); err != nil {
		return err
	}
	s.dropUnmappedColumns(ctx, executor, quotedTable, current)
	return nil
}

func loadColumns(ctx context.Context, executor pgExecutor, table string) (map[string]hermod.ColumnInfo, error) {
	rows, err := executor.Query(ctx, commonQueries[QueryListColumns], table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols := make(map[string]hermod.ColumnInfo)
	for rows.Next() {
		var col hermod.ColumnInfo
		var def *string
		if err := rows.Scan(&col.Name, &col.Type, &col.IsNullable, &col.IsPK, &col.IsIdentity, &def); err != nil {
			return nil, err
		}
		if def != nil {
			col.Default = *def
		}
		cols[col.Name] = col
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return cols, nil
}

func (s *PostgresSink) addOrAlterColumns(ctx context.Context, executor pgExecutor, quotedTable string, current map[string]hermod.ColumnInfo) error {
	for _, m := range s.mappings {
		existing, exists := current[m.TargetColumn]
		if !exists {
			colDef, err := buildColumnDefinition(m)
			if err != nil {
				return err
			}
			if _, err := executor.Exec(ctx, fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s", quotedTable, colDef)); err != nil {
				return err
			}
			continue
		}
		if err := alterColumnType(ctx, executor, quotedTable, m, existing); err != nil {
			return err
		}
	}
	return nil
}

func alterColumnType(ctx context.Context, executor pgExecutor, quotedTable string, m sqlutil.ColumnMapping, existing hermod.ColumnInfo) error {
	dataType, err := baseDataType(m)
	if err != nil {
		return err
	}
	if strings.EqualFold(existing.Type, dataType) || strings.Contains(strings.ToLower(dataType), strings.ToLower(existing.Type)) {
		return nil
	}
	col, err := quoteColumn(m.TargetColumn)
	if err != nil {
		return err
	}
	_, err = executor.Exec(ctx, fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s TYPE %s", quotedTable, col, dataType))
	return err
}

// dropUnmappedColumns removes columns absent from the configured mappings. This
// is a destructive operation gated by autoSync; failures are logged rather than
// aborting the sync so a single protected column cannot stall ingestion.
func (s *PostgresSink) dropUnmappedColumns(ctx context.Context, executor pgExecutor, quotedTable string, current map[string]hermod.ColumnInfo) {
	mapped := make(map[string]bool, len(s.mappings))
	for _, m := range s.mappings {
		mapped[m.TargetColumn] = true
	}
	for name := range current {
		if mapped[name] {
			continue
		}
		col, err := quoteColumn(name)
		if err != nil {
			continue
		}
		if _, err := executor.Exec(ctx, fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s", quotedTable, col)); err != nil {
			if l := s.getLogger(); l != nil {
				l.Warn("postgres sink: failed to drop unmapped column", "column", name, "error", err.Error())
			}
		}
	}
}

// baseDataType returns the validated, non-serial column type used for ALTER ... TYPE.
func baseDataType(m sqlutil.ColumnMapping) (string, error) {
	dataType := m.DataType
	if dataType == "" {
		dataType = "TEXT"
	}
	if err := validateDataType(dataType); err != nil {
		return "", err
	}
	return dataType, nil
}

// convertValue coerces a source value into a representation the pgx driver can
// bind for the target column type. Unrecognised values are returned unchanged.
func (s *PostgresSink) convertValue(val any, dataType string) any {
	if val == nil {
		return nil
	}

	dataType = strings.ToUpper(dataType)

	if strings.Contains(dataType, "JSON") {
		return marshalJSONValue(val)
	}
	if dataType == "UUID" {
		return parseUUIDValue(val)
	}

	str, ok := val.(string)
	if !ok {
		return val
	}
	return coerceStringValue(str, dataType)
}

func marshalJSONValue(val any) any {
	switch v := val.(type) {
	case string:
		return v
	case []byte:
		return v
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return val
		}
		return string(b)
	}
}

func parseUUIDValue(val any) any {
	if str, ok := val.(string); ok {
		if u, err := uuid.Parse(str); err == nil {
			return u
		}
	}
	return val
}

func coerceStringValue(str, dataType string) any {
	switch {
	case strings.Contains(dataType, "INT"):
		if i, err := strconv.ParseInt(str, 10, 64); err == nil {
			return i
		}
	case strings.Contains(dataType, "BOOL"):
		if b, err := strconv.ParseBool(str); err == nil {
			return b
		}
	case strings.Contains(dataType, "FLOAT"), strings.Contains(dataType, "DOUBLE"), strings.Contains(dataType, "NUMERIC"):
		if f, err := strconv.ParseFloat(str, 64); err == nil {
			return f
		}
	case strings.Contains(dataType, "TIMESTAMP"), strings.Contains(dataType, "DATE"):
		return parseTimeValue(str)
	}
	return str
}

func parseTimeValue(str string) any {
	layouts := []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, str); err == nil {
			return t
		}
	}
	return str
}

func (s *PostgresSink) upsertMapped(ctx context.Context, executor pgExecutor, table string, msg hermod.Message) error {
	quoted, err := quoteTable(table)
	if err != nil {
		return fmt.Errorf("invalid table name: %w", err)
	}

	var cols, placeholders, updates, pks []string
	var args []any
	argIdx := 1
	for _, m := range s.mappings {
		val := s.convertValue(evaluator.GetMsgValByPath(msg, m.SourceField), m.DataType)
		if m.IsIdentity && isEmptyIdentity(val) {
			continue
		}
		col, err := quoteColumn(m.TargetColumn)
		if err != nil {
			return err
		}
		cols = append(cols, col)
		placeholders = append(placeholders, fmt.Sprintf("$%d", argIdx))
		args = append(args, val)
		argIdx++
		if m.IsPrimaryKey {
			pks = append(pks, col)
		} else {
			updates = append(updates, fmt.Sprintf("%s = EXCLUDED.%s", col, col))
		}
	}

	if len(cols) == 0 {
		return nil
	}

	query := buildUpsertQuery(quoted, cols, placeholders, pks, updates)
	_, err = executor.Exec(ctx, query, args...)
	return err
}

// buildUpsertQuery composes the INSERT ... ON CONFLICT statement, degrading to a
// plain INSERT (no primary key) or DO NOTHING (only primary-key columns) as needed.
func buildUpsertQuery(quotedTable string, cols, placeholders, pks, updates []string) string {
	insert := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
		quotedTable, strings.Join(cols, ", "), strings.Join(placeholders, ", "))
	if len(pks) == 0 {
		return insert
	}
	if len(updates) == 0 {
		return fmt.Sprintf("%s ON CONFLICT (%s) DO NOTHING", insert, strings.Join(pks, ", "))
	}
	return fmt.Sprintf("%s ON CONFLICT (%s) DO UPDATE SET %s",
		insert, strings.Join(pks, ", "), strings.Join(updates, ", "))
}

func (s *PostgresSink) insertMapped(ctx context.Context, executor pgExecutor, table string, msg hermod.Message) error {
	quoted, err := quoteTable(table)
	if err != nil {
		return fmt.Errorf("invalid table name: %w", err)
	}

	var cols, placeholders []string
	var args []any
	argIdx := 1
	for _, m := range s.mappings {
		val := s.convertValue(evaluator.GetMsgValByPath(msg, m.SourceField), m.DataType)
		if m.IsIdentity && isEmptyIdentity(val) {
			continue
		}
		col, err := quoteColumn(m.TargetColumn)
		if err != nil {
			return err
		}
		cols = append(cols, col)
		placeholders = append(placeholders, fmt.Sprintf("$%d", argIdx))
		args = append(args, val)
		argIdx++
	}

	if len(cols) == 0 {
		return nil
	}

	query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
		quoted, strings.Join(cols, ", "), strings.Join(placeholders, ", "))
	_, err = executor.Exec(ctx, query, args...)
	return err
}

func (s *PostgresSink) updateMapped(ctx context.Context, executor pgExecutor, table string, msg hermod.Message) error {
	quoted, err := quoteTable(table)
	if err != nil {
		return fmt.Errorf("invalid table name: %w", err)
	}

	var updates []string
	var args []any
	argIdx := 1
	for _, m := range s.mappings {
		if m.IsPrimaryKey {
			continue
		}
		col, err := quoteColumn(m.TargetColumn)
		if err != nil {
			return err
		}
		val := s.convertValue(evaluator.GetMsgValByPath(msg, m.SourceField), m.DataType)
		updates = append(updates, fmt.Sprintf("%s = $%d", col, argIdx))
		args = append(args, val)
		argIdx++
	}

	pks, pkArgs, err := s.primaryKeyPredicates(msg, argIdx)
	if err != nil {
		return err
	}
	if len(pks) == 0 {
		return errors.New("cannot update without primary key mappings")
	}
	if len(updates) == 0 {
		return nil
	}

	args = append(args, pkArgs...)
	query := fmt.Sprintf("UPDATE %s SET %s WHERE %s",
		quoted, strings.Join(updates, ", "), strings.Join(pks, " AND "))
	_, err = executor.Exec(ctx, query, args...)
	return err
}
