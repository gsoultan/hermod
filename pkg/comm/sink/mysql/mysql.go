package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
	"github.com/gsoultan/hermod/pkg/infra/sqlutil"
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

	// xa is the XA transaction open for a transactional group, if any; see
	// xa.go. Guarded by xaMu rather than mu, which ensureTable holds across
	// whole statements.
	xaMu sync.Mutex
	xa   *xaTxn
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

// qcol quotes a mapped column name, refusing one that cannot be quoted safely.
//
// These were wrapped in backticks with fmt.Sprintf, which neither validates the
// name nor escapes a backtick inside it — so a name carrying one ended its own
// identifier and the remainder became statement text. Confirmed against a real
// server: a column named "name`, (SELECT 1)) -- " produced
// "`name`, (SELECT 1)) -- `" in the statement, and MySQL rejected it only
// because that particular text happened to be invalid SQL. A name chosen to
// parse would not have been.
//
// sqlutil.QuoteIdent validates the name and doubles any backtick, which is the
// rule SECURITY.md states and which the rest of the SQL sinks follow.
func qcol(name string) (string, error) {
	quoted, err := sqlutil.QuoteIdent("mysql", name)
	if err != nil {
		return "", fmt.Errorf("invalid column name %q: %w", name, err)
	}
	return quoted, nil
}

func (s *MySQLSink) Write(ctx context.Context, msg hermod.Message) error {
	return s.WriteBatch(ctx, []hermod.Message{msg})
}

func (s *MySQLSink) WriteBatch(ctx context.Context, msgs []hermod.Message) error {
	if len(msgs) == 0 {
		return nil
	}
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

	// Inside a transactional group the writes join the open XA transaction on
	// its dedicated connection, and nothing is committed here: the coordinator
	// decides. Table DDL goes through the pool instead, because MySQL refuses
	// any statement that implicitly commits while an XA transaction is ACTIVE
	// (XAER_RMFAIL) — and MySQL DDL is never transactional, so running it inside
	// the transaction would not have made it atomic anyway.
	if xa := s.currentXA(); xa != nil {
		return s.applyBatch(ctx, xa.conn, db, msgs)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin mysql transaction: %w", err)
	}
	defer tx.Rollback()

	if err := s.applyBatch(ctx, tx, tx, msgs); err != nil {
		return err
	}
	return tx.Commit()
}

// applyBatch writes msgs through tx, running any table DDL through ddl. It
// commits nothing; the caller owns the transaction.
func (s *MySQLSink) applyBatch(ctx context.Context, tx, ddl sqlExecutor, msgs []hermod.Message) error {
	// A batch that is purely inserts into one table can go as multi-row
	// INSERTs instead of one statement per message — for a remote database
	// that is the difference between N round trips and a handful, which
	// dominates everything else a sink does. classifyBatch admits a batch only
	// when ordering cannot be observed; anything else, including anything it
	// cannot establish, falls through to the loop below unchanged.
	if s.classifyBatch(msgs) == bulkModeMultiValues {
		if err := s.ensureTable(ctx, ddl, s.tableName); err != nil {
			return fmt.Errorf("ensure table %s: %w", s.tableName, err)
		}
		done, err := s.writeBatchMultiValues(ctx, tx, s.tableName, msgs)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		// buildBulkRows declined (the batch has no single tuple shape). Fall
		// through: the ordered path handles it, and ensureTable above is
		// idempotent.
	}

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
		table := s.messageTable(msg)
		if err := s.ensureTable(ctx, ddl, table); err != nil {
			return fmt.Errorf("ensure table %s: %w", table, err)
		}
		if err := s.applyMessage(ctx, tx, stmts, table, msg); err != nil {
			return fmt.Errorf("mysql batch write error on message %s: %w", msg.ID(), err)
		}
	}
	return nil
}

// messageTable is the configured table, or the message's own when none is.
func (s *MySQLSink) messageTable(msg hermod.Message) string {
	if s.tableName != "" {
		return s.tableName
	}
	if msg.Schema() != "" {
		return fmt.Sprintf("%s.%s", msg.Schema(), msg.Table())
	}
	return msg.Table()
}

// messageOperation is the operation to apply: the message's own, unless the
// sink is configured to force one.
func (s *MySQLSink) messageOperation(msg hermod.Message) hermod.Operation {
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

// applyMessage applies one message, reusing prepared statements from stmts.
func (s *MySQLSink) applyMessage(ctx context.Context, tx sqlExecutor, stmts map[string]*sql.Stmt, table string, msg hermod.Message) error {
	switch op := s.messageOperation(msg); op {
	case hermod.OpCreate, hermod.OpSnapshot, hermod.OpUpdate:
		if len(s.mappings) == 0 {
			return execCached(ctx, tx, stmts, "upsert", QueryUpsert, table, msg.ID(), msg.Payload())
		}
		switch s.operationMode {
		case "insert":
			return s.insertMapped(ctx, tx, table, msg)
		case "update":
			return s.updateMapped(ctx, tx, table, msg)
		default:
			return s.upsertMapped(ctx, tx, table, msg)
		}
	case hermod.OpDelete:
		if s.deleteStrategy == "ignore" {
			return nil
		}
		if len(s.mappings) > 0 {
			return s.deleteMapped(ctx, tx, table, msg)
		}
		return execCached(ctx, tx, stmts, "delete", QueryDelete, table, msg.ID())
	default:
		return fmt.Errorf("unsupported operation: %s", op)
	}
}

// execCached runs one of the unmapped statements for table, preparing it once
// per batch.
func execCached(ctx context.Context, tx sqlExecutor, stmts map[string]*sql.Stmt, kind, query, table string, args ...any) error {
	key := kind + ":" + table
	st := stmts[key]
	if st == nil {
		var err error
		st, err = tx.PrepareContext(ctx, fmt.Sprintf(commonQueries[query], table))
		if err != nil {
			return fmt.Errorf("prepare %s failed: %w", kind, err)
		}
		stmts[key] = st
	}
	_, err := st.ExecContext(ctx, args...)
	return err
}

func (s *MySQLSink) init(ctx context.Context) error {
	s.mu.Lock()
	if s.db != nil {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	db, err := sql.Open("mysql", s.connString)
	if err != nil {
		return fmt.Errorf("failed to connect to mysql: %w", err)
	}
	// Conservative pool defaults
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(10)
	db.SetConnMaxIdleTime(60 * time.Second)

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

func (s *MySQLSink) Ping(ctx context.Context) error {
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

func (s *MySQLSink) Close() error {
	// An XA transaction still open was never prepared; ending its session rolls
	// it back. Prepared ones are not held here and are unaffected.
	if xa := s.takeXA(); xa != nil {
		discardConn(xa.conn)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db != nil {
		err := s.db.Close()
		s.db = nil
		return err
	}
	return nil
}

func (s *MySQLSink) DiscoverDatabases(ctx context.Context) ([]string, error) {
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

	rows, err := db.QueryContext(ctx, commonQueries[QueryShowDatabases])
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

	rows, err := db.QueryContext(ctx, commonQueries[QueryShowTables])
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

	rows, err := db.QueryContext(ctx, commonQueries[QueryListColumns], table)
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

		values := make([]any, len(cols))
		valuePtrs := make([]any, len(cols))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			return nil, err
		}

		record := make(map[string]any)
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

func (s *MySQLSink) deleteMapped(ctx context.Context, tx sqlExecutor, table string, msg hermod.Message) error {
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
			q, err := qcol(m.TargetColumn)
			if err != nil {
				return err
			}
			pks = append(pks, q+" = ?")
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

func (s *MySQLSink) ensureTable(ctx context.Context, tx sqlExecutor, table string) error {
	if _, ok := s.verifiedTables.Load(table); ok {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.verifiedTables.Load(table); ok {
		return nil
	}

	// In MySQL, "schema" is often a database name
	dbName, tableNameOnly := "", table
	if strings.Contains(table, ".") {
		parts := strings.SplitN(table, ".", 2)
		dbName, tableNameOnly = parts[0], parts[1]
		if err := ensureDatabase(ctx, tx, dbName); err != nil {
			return err
		}
	}

	quotedTable, err := sqlutil.QuoteIdent("mysql", table)
	if err != nil {
		return fmt.Errorf("invalid table name: %w", err)
	}

	if tableExists(ctx, tx, dbName, tableNameOnly) {
		err = s.reconcileExistingTable(ctx, tx, table, quotedTable, dbName, tableNameOnly)
	} else {
		err = s.createTable(ctx, tx, quotedTable)
	}
	if err != nil {
		return err
	}

	s.verifiedTables.Store(table, true)
	return nil
}

// ensureDatabase creates dbName if it is missing. A failure to create it is
// ignored, as the account may lack the permission and the database may exist.
func ensureDatabase(ctx context.Context, tx sqlExecutor, dbName string) error {
	quotedDB, err := sqlutil.QuoteIdent("mysql", dbName)
	if err != nil {
		return fmt.Errorf("invalid database name: %w", err)
	}
	_, _ = tx.ExecContext(ctx, fmt.Sprintf(commonQueries[QueryCreateDatabase], quotedDB))
	return nil
}

// tableExists reports whether the table exists; a failed lookup counts as no.
func tableExists(ctx context.Context, tx sqlExecutor, dbName, tableNameOnly string) bool {
	checkQuery := "SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_NAME = ?"
	checkArgs := []any{tableNameOnly}
	if dbName != "" {
		checkQuery += " AND TABLE_SCHEMA = ?"
		checkArgs = append(checkArgs, dbName)
	} else {
		checkQuery += " AND TABLE_SCHEMA = DATABASE()"
	}
	var count int
	if err := tx.QueryRowContext(ctx, checkQuery, checkArgs...).Scan(&count); err != nil {
		return false
	}
	return count > 0
}

// reconcileExistingTable applies truncate_table and sync_columns to a table
// that already exists.
func (s *MySQLSink) reconcileExistingTable(ctx context.Context, tx sqlExecutor, table, quotedTable, dbName, tableNameOnly string) error {
	if s.autoTruncate {
		if _, err := tx.ExecContext(ctx, "TRUNCATE TABLE "+quotedTable); err != nil {
			return fmt.Errorf("truncate table %s: %w", table, err)
		}
	}
	if s.autoSync && len(s.mappings) > 0 {
		if err := s.syncColumns(ctx, tx, table, dbName, tableNameOnly); err != nil {
			return fmt.Errorf("sync columns %s: %w", table, err)
		}
	}
	return nil
}

// createTable creates the table from the column mappings, or the default
// id/data shape when there are none.
func (s *MySQLSink) createTable(ctx context.Context, tx sqlExecutor, quotedTable string) error {
	tableQuery := fmt.Sprintf(commonQueries[QueryCreateTable], quotedTable)
	if len(s.mappings) > 0 {
		cols := make([]string, 0, len(s.mappings))
		for _, m := range s.mappings {
			cols = append(cols, mappingColumnDef(m))
		}
		tableQuery = fmt.Sprintf("CREATE TABLE %s (%s)", quotedTable, strings.Join(cols, ", "))
	}
	if _, err := tx.ExecContext(ctx, tableQuery); err != nil {
		return fmt.Errorf("create table: %w", err)
	}
	return nil
}

// mappingDataType is the mapping's column type, TEXT when unset.
func mappingDataType(m sqlutil.ColumnMapping) string {
	if m.DataType == "" {
		return "TEXT"
	}
	return m.DataType
}

// mappingColumnDef is the column definition a mapping creates.
func mappingColumnDef(m sqlutil.ColumnMapping) string {
	colDef := fmt.Sprintf("`%s` %s", m.TargetColumn, mappingDataType(m))
	if m.IsIdentity {
		colDef += " AUTO_INCREMENT"
	}
	if m.IsPrimaryKey {
		colDef += " PRIMARY KEY"
	} else if !m.IsNullable {
		colDef += " NOT NULL"
	}
	return colDef
}

func (s *MySQLSink) syncColumns(ctx context.Context, tx sqlExecutor, table, dbName, tableNameOnly string) error {
	currentCols, err := listColumns(ctx, tx, dbName, tableNameOnly)
	if err != nil {
		return err
	}

	quotedTable, _ := sqlutil.QuoteIdent("mysql", table)

	// Add or Modify columns
	for _, m := range s.mappings {
		col, exists := currentCols[m.TargetColumn]
		dataType := mappingDataType(m)
		var alterQuery string
		switch {
		case !exists:
			alterQuery = fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s", quotedTable, mappingColumnDef(m))
		case !strings.EqualFold(col.Type, dataType) && !strings.Contains(strings.ToLower(dataType), strings.ToLower(col.Type)):
			// Basic type check
			alterQuery = fmt.Sprintf("ALTER TABLE %s MODIFY COLUMN `%s` %s", quotedTable, m.TargetColumn, dataType)
		default:
			continue
		}
		if _, err := tx.ExecContext(ctx, alterQuery); err != nil {
			return err
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

// listColumns reads the table's current columns, keyed by name.
func listColumns(ctx context.Context, tx sqlExecutor, dbName, tableNameOnly string) (map[string]hermod.ColumnInfo, error) {
	query := commonQueries[QueryListColumns]
	args := []any{tableNameOnly}
	if dbName != "" {
		query = strings.Replace(query, "TABLE_SCHEMA = DATABASE()", "TABLE_SCHEMA = ?", 1)
		args = append(args, dbName)
	}

	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	currentCols := make(map[string]hermod.ColumnInfo)
	for rows.Next() {
		var col hermod.ColumnInfo
		var def *string
		if err := rows.Scan(&col.Name, &col.Type, &col.IsNullable, &col.IsPK, &col.IsIdentity, &def); err != nil {
			return nil, err
		}
		if def != nil {
			col.Default = *def
		}
		currentCols[col.Name] = col
	}
	return currentCols, rows.Err()
}

func (s *MySQLSink) upsertMapped(ctx context.Context, tx sqlExecutor, table string, msg hermod.Message) error {
	data := msg.Data()
	if data == nil {
		if err := json.Unmarshal(msg.Payload(), &data); err != nil {
			return fmt.Errorf("failed to parse message data: %w", err)
		}
	}

	var cols, placeholders, updates []string
	var args []any
	hasPK := false

	for _, m := range s.mappings {
		if m.SourceField == "" {
			continue
		}
		val := evaluator.GetMsgValByPath(msg, m.SourceField)

		if m.IsIdentity && (val == nil || val == "" || val == 0) {
			continue
		}

		q, err := qcol(m.TargetColumn)
		if err != nil {
			return err
		}
		cols = append(cols, q)
		placeholders = append(placeholders, "?")
		args = append(args, val)

		if m.IsPrimaryKey {
			hasPK = true
		} else {
			updates = append(updates, q+" = VALUES("+q+")")
		}
	}

	query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
		table, strings.Join(cols, ", "), strings.Join(placeholders, ", "))
	if hasPK {
		query += " ON DUPLICATE KEY UPDATE " + strings.Join(updates, ", ")
	}
	_, err := tx.ExecContext(ctx, query, args...)
	return err
}

func (s *MySQLSink) insertMapped(ctx context.Context, tx sqlExecutor, table string, msg hermod.Message) error {
	var cols []string
	var placeholders []string
	var args []any

	for _, m := range s.mappings {
		if m.SourceField == "" {
			continue
		}
		val := evaluator.GetMsgValByPath(msg, m.SourceField)
		if m.IsIdentity && (val == nil || val == "" || val == 0) {
			continue
		}
		q, err := qcol(m.TargetColumn)
		if err != nil {
			return err
		}
		cols = append(cols, q)
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

func (s *MySQLSink) updateMapped(ctx context.Context, tx sqlExecutor, table string, msg hermod.Message) error {
	var updates []string
	var pks []string
	var args []any
	var pkArgs []any

	for _, m := range s.mappings {
		if m.SourceField == "" {
			continue
		}
		val := evaluator.GetMsgValByPath(msg, m.SourceField)
		q, err := qcol(m.TargetColumn)
		if err != nil {
			return err
		}
		if m.IsPrimaryKey {
			pks = append(pks, q+" = ?")
			pkArgs = append(pkArgs, val)
		} else {
			updates = append(updates, q+" = ?")
			args = append(args, val)
		}
	}

	if len(pks) == 0 {
		return errors.New("cannot update without primary key mappings")
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
