package mssql

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	_ "github.com/microsoft/go-mssqldb"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

// MSSQLSource implements the hermod.Source interface for MSSQL CDC.
type MSSQLSource struct {
	connString    string
	db            *sql.DB
	tables        []string
	pollInterval  time.Duration
	autoEnableCDC bool
	useCDC        bool

	mu        sync.Mutex
	tableLSNs map[string][]byte // table name -> last acked LSN
	buffer    []hermod.Message
	captures  map[string]string // table name -> capture instance name
	logger    hermod.Logger
	msgChan   chan hermod.Message
}

func NewMSSQLSource(connString string, tables []string, autoEnableCDC bool, useCDC bool) *MSSQLSource {
	normalizedTables := make([]string, len(tables))
	for i, t := range tables {
		normalizedTables[i] = normalizeTableName(t)
	}

	return &MSSQLSource{
		connString:    connString,
		tables:        normalizedTables,
		pollInterval:  5 * time.Second,
		autoEnableCDC: autoEnableCDC,
		useCDC:        useCDC,
		tableLSNs:     make(map[string][]byte),
		captures:      make(map[string]string),
		msgChan:       make(chan hermod.Message, 1000),
	}
}

func (m *MSSQLSource) SetLogger(logger hermod.Logger) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logger = logger
}

func (m *MSSQLSource) log(level, msg string, keysAndValues ...interface{}) {
	m.mu.Lock()
	logger := m.logger
	m.mu.Unlock()

	if logger == nil {
		// Fallback to standard log if no structured logger is set, to ensure timestamps
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

func normalizeTableName(table string) string {
	parts := strings.Split(table, ".")
	for i, p := range parts {
		parts[i] = strings.Trim(p, "[] ")
	}
	return strings.Join(parts, ".")
}

type tableInfo struct {
	objectID int32
	schema   string
	name     string
}

func (m *MSSQLSource) resolveTable(ctx context.Context, table string) (*tableInfo, error) {
	m.mu.Lock()
	db := m.db
	m.mu.Unlock()
	if db == nil {
		return nil, fmt.Errorf("database connection not initialized")
	}

	var info tableInfo
	// OBJECT_ID is collation-aware and handles 1, 2, or 3 part names.
	err := db.QueryRowContext(ctx, queryResolveTableByID, table).Scan(&info.objectID, &info.schema, &info.name)

	if err == nil {
		return &info, nil
	}

	if err != sql.ErrNoRows {
		return nil, fmt.Errorf("failed to resolve table %s: %w", table, err)
	}

	// If OBJECT_ID failed, try a more liberal search (case-insensitive and potentially missing schema)
	parts := strings.Split(table, ".")
	tableName := parts[len(parts)-1]

	var rows *sql.Rows
	if len(parts) >= 2 {
		schemaName := parts[len(parts)-2]
		rows, err = db.QueryContext(ctx, queryResolveTableByFullQualifiedName, schemaName, tableName)
	} else {
		rows, err = db.QueryContext(ctx, queryResolveTableByNameOnly, tableName)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to search for table %s: %w", table, err)
	}
	defer rows.Close()

	if rows.Next() {
		if err := rows.Scan(&info.objectID, &info.schema, &info.name); err != nil {
			return nil, err
		}
		return &info, nil
	}

	return nil, fmt.Errorf("table %s not found", table)
}

func (m *MSSQLSource) Read(ctx context.Context) (hermod.Message, error) {
	if !m.useCDC {
		m.mu.Lock()
		db := m.db
		m.mu.Unlock()
		if db == nil {
			if err := m.Ping(ctx); err != nil {
				return nil, err
			}
		}

		select {
		case msg := <-m.msgChan:
			return msg, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	for {
		select {
		case msg := <-m.msgChan:
			return msg, nil
		default:
		}

		m.mu.Lock()
		if len(m.buffer) > 0 {
			msg := m.buffer[0]
			m.buffer = m.buffer[1:]
			m.mu.Unlock()
			return msg, nil
		}
		m.mu.Unlock()

		if err := m.poll(ctx); err != nil {
			return nil, err
		}

		m.mu.Lock()
		if len(m.buffer) == 0 {
			m.mu.Unlock()
			select {
			case msg := <-m.msgChan:
				return msg, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(m.pollInterval):
				continue
			}
		}
		m.mu.Unlock()
	}
}

func (m *MSSQLSource) Snapshot(ctx context.Context, tables ...string) error {
	m.mu.Lock()
	db := m.db
	m.mu.Unlock()
	if db == nil {
		if err := m.Ping(ctx); err != nil {
			return err
		}
	}

	targetTables := tables
	if len(targetTables) == 0 {
		targetTables = m.tables
	}

	if len(targetTables) == 0 {
		var err error
		targetTables, err = m.DiscoverTables(ctx)
		if err != nil {
			return err
		}
	}

	for _, table := range targetTables {
		if err := m.snapshotTable(ctx, table); err != nil {
			return err
		}
	}
	return nil
}

func (m *MSSQLSource) snapshotTable(ctx context.Context, table string) error {
	m.mu.Lock()
	db := m.db
	m.mu.Unlock()
	if db == nil {
		return fmt.Errorf("database connection not initialized")
	}

	// MSSQL uses [] for quoting
	parts := strings.Split(table, ".")
	quotedParts := make([]string, len(parts))
	for i, p := range parts {
		quotedParts[i] = "[" + strings.Trim(p, "[] ") + "]"
	}
	quoted := strings.Join(quotedParts, ".")

	rows, err := db.QueryContext(ctx, fmt.Sprintf("SELECT * FROM %s", quoted))
	if err != nil {
		return fmt.Errorf("failed to query table %q: %w", table, err)
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return err
	}

	for rows.Next() {
		values := make([]interface{}, len(columns))
		valuePtrs := make([]interface{}, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			return err
		}

		record := make(map[string]interface{})
		for i, colName := range columns {
			val := values[i]
			if b, ok := val.([]byte); ok {
				record[colName] = string(b)
			} else {
				record[colName] = val
			}
		}

		afterJSON, _ := json.Marshal(message.SanitizeMap(record))

		msg := message.AcquireMessage()
		msg.SetID(fmt.Sprintf("snapshot-%s-%d", table, time.Now().UnixNano()))
		msg.SetOperation(hermod.OpSnapshot)
		msg.SetTable(table)
		msg.SetAfter(afterJSON)
		msg.SetMetadata("source", "mssql")
		msg.SetMetadata("snapshot", "true")

		select {
		case m.msgChan <- msg:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return rows.Err()
}

func (m *MSSQLSource) poll(ctx context.Context) error {
	if err := m.Ping(ctx); err != nil {
		return err
	}

	m.mu.Lock()
	db := m.db
	m.mu.Unlock()
	if db == nil {
		return fmt.Errorf("database connection not initialized")
	}

	if len(m.captures) == 0 {
		if err := m.discoverCaptures(ctx); err != nil {
			return err
		}
	}

	var maxLSN []byte
	err := db.QueryRowContext(ctx, queryGetMaxLSN).Scan(&maxLSN)
	if err != nil {
		return fmt.Errorf("failed to get max lsn: %w", err)
	}

	if maxLSN == nil {
		return nil
	}

	var allMessages []hermod.Message
	for table, capture := range m.captures {
		msgs, err := m.pollTable(ctx, table, capture, maxLSN)
		if err != nil {
			m.mu.Lock()
			m.captures = make(map[string]string)
			m.mu.Unlock()
			return err
		}
		allMessages = append(allMessages, msgs...)
	}

	if len(allMessages) > 0 {
		m.sortAndBufferMessages(allMessages)
	}

	return nil
}

func (m *MSSQLSource) sortAndBufferMessages(msgs []hermod.Message) {
	// Sort by LSN string (metadata) which is hex representation of binary LSN
	sort.Slice(msgs, func(i, j int) bool {
		return msgs[i].Metadata()["lsn"] < msgs[j].Metadata()["lsn"]
	})

	m.mu.Lock()
	m.buffer = append(m.buffer, msgs...)
	m.mu.Unlock()
}

func (m *MSSQLSource) discoverCaptures(ctx context.Context) error {
	var configToID map[string]int32
	var resolveErrors map[string]error
	var err error

	// Retry discovery a few times as CDC enablement is asynchronous in MSSQL
	for attempt := 1; attempt <= 5; attempt++ {
		configToID, resolveErrors, err = m.resolveTables(ctx)
		if err != nil {
			return err
		}

		m.mu.Lock()
		db := m.db
		m.mu.Unlock()
		if db == nil {
			return fmt.Errorf("database connection not initialized")
		}

		rows, err := db.QueryContext(ctx, queryDiscoverCaptures)
		if err != nil {
			return fmt.Errorf("failed to discover capture instances: %w", err)
		}

		err = m.assignCaptures(rows, configToID, resolveErrors)
		rows.Close()

		if err == nil {
			return nil
		}

		if attempt < 5 {
			m.log("WARN", "Capture instances not ready yet, retrying...", "attempt", attempt, "error", err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * time.Second):
			}
		}
	}

	return err
}

func (m *MSSQLSource) resolveTables(ctx context.Context) (map[string]int32, map[string]error, error) {
	configToID := make(map[string]int32)
	resolveErrors := make(map[string]error)

	for _, t := range m.tables {
		info, err := m.ensureTableCDC(ctx, t)
		if err == nil {
			configToID[t] = info.objectID
		} else {
			resolveErrors[t] = err
		}
	}

	return configToID, resolveErrors, nil
}

func (m *MSSQLSource) assignCaptures(rows *sql.Rows, configToID map[string]int32, resolveErrors map[string]error) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Reset captures map to ensure we only have what's currently found and configured
	m.captures = make(map[string]string)
	matchedConfigured := make(map[string]bool)

	for rows.Next() {
		var schemaName, tableName, captureInstance string
		var sourceObjectID int32
		if err := rows.Scan(&schemaName, &tableName, &captureInstance, &sourceObjectID); err != nil {
			return err
		}
		fullTableName := schemaName + "." + tableName

		if len(m.tables) == 0 {
			m.captures[fullTableName] = captureInstance
		} else {
			// Match this found table against any of our configured tables
			for _, t := range m.tables {
				// 1. Match by object ID
				if id, ok := configToID[t]; ok && id == sourceObjectID {
					m.captures[fullTableName] = captureInstance
					matchedConfigured[t] = true
					continue
				}
				// 2. Match by name
				if m.matchTable(t, schemaName, tableName) {
					m.captures[fullTableName] = captureInstance
					matchedConfigured[t] = true
				}
			}
		}
	}

	if len(m.tables) > 0 {
		missing := []string{}
		for _, t := range m.tables {
			if !matchedConfigured[t] {
				if err, ok := resolveErrors[t]; ok {
					missing = append(missing, fmt.Sprintf("%s (%v)", t, err))
				} else {
					missing = append(missing, t)
				}
			}
		}
		if len(missing) > 0 {
			return fmt.Errorf("no capture instances found for tables: %v", missing)
		}
	}

	return nil
}

func (m *MSSQLSource) matchTable(configured, physicalSchema, physicalTable string) bool {
	conf := strings.ToLower(normalizeTableName(configured))
	physSchema := strings.ToLower(physicalSchema)
	physTable := strings.ToLower(physicalTable)
	physFull := physSchema + "." + physTable

	// Exact match
	if conf == physFull {
		return true
	}

	// Match by table name if no schema in configured
	if !strings.Contains(conf, ".") && conf == physTable {
		return true
	}

	// Match by components from right to left (handles 3-part names)
	confParts := strings.Split(conf, ".")
	if confParts[len(confParts)-1] == physTable {
		if len(confParts) == 1 {
			return true
		}
		if len(confParts) >= 2 && confParts[len(confParts)-2] == physSchema {
			return true
		}
	}

	return false
}

func (m *MSSQLSource) ensureDatabaseCDC(ctx context.Context) error {
	m.mu.Lock()
	db := m.db
	m.mu.Unlock()
	if db == nil {
		return fmt.Errorf("database connection not initialized")
	}

	var isCDCEnabled bool
	err := db.QueryRowContext(ctx, queryCheckDatabaseCDC).Scan(&isCDCEnabled)
	if err != nil {
		return fmt.Errorf("failed to check if CDC is enabled on database: %w", err)
	}

	if !isCDCEnabled {
		_, err = db.ExecContext(ctx, queryEnableDatabaseCDC)
		if err != nil {
			return fmt.Errorf("failed to enable CDC on database: %w", err)
		}
		m.log("INFO", "Enabled CDC on database")
	}
	return nil
}

func (m *MSSQLSource) ensureTableCDC(ctx context.Context, table string) (*tableInfo, error) {
	// 1. Resolve table first to get schema and name
	info, err := m.resolveTable(ctx, table)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	db := m.db
	m.mu.Unlock()
	if db == nil {
		return nil, fmt.Errorf("database connection not initialized")
	}

	// 2. Check if CDC is enabled on the database
	var isDatabaseCDCEnabled bool
	err = db.QueryRowContext(ctx, queryCheckDatabaseCDC).Scan(&isDatabaseCDCEnabled)
	if err != nil {
		return nil, fmt.Errorf("failed to check if CDC is enabled on database: %w", err)
	}

	if !isDatabaseCDCEnabled {
		if !m.autoEnableCDC {
			return nil, fmt.Errorf("CDC is not enabled on database and auto_enable_cdc is false")
		}
		if err := m.ensureDatabaseCDC(ctx); err != nil {
			return nil, err
		}
	}

	// 3. Check if CDC is already enabled for the table
	var isTracked bool
	err = db.QueryRowContext(ctx, queryCheckTableCDC, info.objectID).Scan(&isTracked)

	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("failed to check if CDC is enabled on table %s.%s: %w", info.schema, info.name, err)
	}

	if err == sql.ErrNoRows || !isTracked {
		if !m.autoEnableCDC {
			return nil, fmt.Errorf("CDC is not enabled for table %s.%s and auto_enable_cdc is false", info.schema, info.name)
		}
		// Use named parameters for clarity and set supports_net_changes to 0 for better compatibility
		_, err = db.ExecContext(ctx, queryEnableTableCDC, info.schema, info.name)
		if err != nil {
			return nil, fmt.Errorf("failed to enable CDC on table %s.%s: %w", info.schema, info.name, err)
		}
		m.log("INFO", "Enabled CDC on table", "schema", info.schema, "table", info.name)
	}

	return info, nil
}

func (m *MSSQLSource) pollTable(ctx context.Context, table, capture string, maxLSN []byte) ([]hermod.Message, error) {
	m.mu.Lock()
	db := m.db
	m.mu.Unlock()
	if db == nil {
		return nil, fmt.Errorf("database connection not initialized")
	}

	var minLSN []byte
	err := db.QueryRowContext(ctx, queryGetMinLSN, capture).Scan(&minLSN)
	if err != nil {
		return nil, fmt.Errorf("failed to get min lsn for %s: %w", capture, err)
	}

	m.mu.Lock()
	fromLSN := m.tableLSNs[table]
	m.mu.Unlock()

	if fromLSN == nil {
		fromLSN = minLSN
	} else {
		// Use the max of lastLSN and minLSN to handle log truncation
		if bytes.Compare(fromLSN, minLSN) < 0 {
			fromLSN = minLSN
		} else {
			err = db.QueryRowContext(ctx, queryIncrementLSN, fromLSN).Scan(&fromLSN)
			if err != nil {
				return nil, fmt.Errorf("failed to increment lsn: %w", err)
			}
		}
	}

	if bytes.Compare(fromLSN, maxLSN) > 0 {
		return nil, nil
	}

	query := fmt.Sprintf(queryGetTableChangesFormat, capture)
	rows, err := db.QueryContext(ctx, query, fromLSN, maxLSN)
	if err != nil {
		return nil, fmt.Errorf("failed to query changes for %s: %w", capture, err)
	}
	defer rows.Close()

	return m.processChangeRows(table, rows)
}

func (m *MSSQLSource) processChangeRows(table string, rows *sql.Rows) ([]hermod.Message, error) {
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	var msgs []hermod.Message
	var lastBeforeMsg *message.DefaultMessage

	for rows.Next() {
		values := make([]interface{}, len(columns))
		valuePtrs := make([]interface{}, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			return nil, err
		}

		entry := make(map[string]interface{})
		var rowLSN []byte
		var rowSeq []byte
		var operation int
		for i, colName := range columns {
			val := values[i]
			if b, ok := val.([]byte); ok && colName == "__$start_lsn" {
				rowLSN = b
			}
			if b, ok := val.([]byte); ok && colName == "__$seqval" {
				rowSeq = b
			}
			if colName == "__$operation" {
				if v, ok := val.(int64); ok {
					operation = int(v)
				} else if v, ok := val.(int32); ok {
					operation = int(v)
				}
			}

			if !strings.HasPrefix(colName, "__$") {
				entry[colName] = val
			}
		}

		msg := m.mapToMessage(table, operation, rowLSN, rowSeq, entry)

		// Merge Update Before (3) and After (4) if they have same LSN and Seq
		if operation == 3 {
			if lastBeforeMsg != nil {
				msgs = append(msgs, lastBeforeMsg)
			}
			lastBeforeMsg = msg
			continue
		}

		if operation == 4 && lastBeforeMsg != nil {
			if lastBeforeMsg.Metadata()["lsn"] == msg.Metadata()["lsn"] &&
				lastBeforeMsg.Metadata()["seqval"] == msg.Metadata()["seqval"] {
				msg.SetBefore(lastBeforeMsg.Before())
				msgs = append(msgs, msg)
				message.ReleaseMessage(lastBeforeMsg)
				lastBeforeMsg = nil
				continue
			} else {
				msgs = append(msgs, lastBeforeMsg)
				lastBeforeMsg = nil
			}
		}

		if lastBeforeMsg != nil {
			msgs = append(msgs, lastBeforeMsg)
			lastBeforeMsg = nil
		}

		msgs = append(msgs, msg)
	}

	if lastBeforeMsg != nil {
		msgs = append(msgs, lastBeforeMsg)
	}

	return msgs, nil
}

func (m *MSSQLSource) mapToMessage(table string, op int, lsn []byte, seq []byte, data map[string]interface{}) *message.DefaultMessage {
	msg := message.AcquireMessage()

	schema, tableName := parseTableParts(table)
	msg.SetSchema(schema)
	msg.SetTable(tableName)

	lsnHex := hex.EncodeToString(lsn)
	seqHex := hex.EncodeToString(seq)
	msg.SetID(fmt.Sprintf("%s-%s-%d", lsnHex, seqHex, op))
	msg.SetMetadata("lsn", lsnHex)
	msg.SetMetadata("seqval", seqHex)
	msg.SetMetadata("source", "mssql")

	jsonBytes, _ := json.Marshal(message.SanitizeMap(data))
	m.setMsgOperation(msg, op, jsonBytes)

	return msg
}

func parseTableParts(table string) (string, string) {
	schema := "dbo"
	tableName := table
	if parts := strings.Split(table, "."); len(parts) == 2 {
		schema = parts[0]
		tableName = parts[1]
	}
	return schema, tableName
}

func (m *MSSQLSource) setMsgOperation(msg *message.DefaultMessage, op int, data []byte) {
	switch op {
	case 1: // delete
		msg.SetOperation(hermod.OpDelete)
		msg.SetBefore(data)
	case 2: // insert
		msg.SetOperation(hermod.OpCreate)
		msg.SetAfter(data)
	case 3: // update (before image)
		msg.SetOperation(hermod.OpUpdate)
		msg.SetBefore(data)
	case 4: // update (after image)
		msg.SetOperation(hermod.OpUpdate)
		msg.SetAfter(data)
	}
}

func (m *MSSQLSource) SetState(state map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.tableLSNs == nil {
		m.tableLSNs = make(map[string][]byte)
	}
	for table, lsnHex := range state {
		if lsn, err := hex.DecodeString(lsnHex); err == nil {
			m.tableLSNs[table] = lsn
		}
	}
}

func (m *MSSQLSource) GetState() map[string]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := make(map[string]string)
	for table, lsn := range m.tableLSNs {
		state[table] = hex.EncodeToString(lsn)
	}
	return state
}

func (m *MSSQLSource) Ack(ctx context.Context, msg hermod.Message) error {
	if msg == nil {
		return nil
	}
	lsnHex := msg.Metadata()["lsn"]
	if lsnHex == "" {
		return nil
	}

	lsn, err := hex.DecodeString(lsnHex)
	if err != nil {
		return fmt.Errorf("failed to parse LSN: %w", err)
	}

	table := msg.Table()
	schema := msg.Schema()
	fullTable := table
	if schema != "" {
		fullTable = schema + "." + table
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.tableLSNs == nil {
		m.tableLSNs = make(map[string][]byte)
	}

	current := m.tableLSNs[fullTable]
	if current == nil || bytes.Compare(lsn, current) > 0 {
		m.tableLSNs[fullTable] = lsn
	}

	return nil
}

func (m *MSSQLSource) Ping(ctx context.Context) error {
	// For health checks, we only want to verify connectivity, not full CDC status
	// which might trigger auto-enabling CDC or heavy metadata queries.
	return m.ping(ctx, false)
}

func (m *MSSQLSource) IsReady(ctx context.Context) error {
	// For readiness checks used by the engine, we perform a deep check of CDC status
	return m.ping(ctx, true)
}

func (m *MSSQLSource) ping(ctx context.Context, checkCDC bool) error {
	m.mu.Lock()
	if m.db == nil {
		db, err := sql.Open("sqlserver", m.connString)
		if err != nil {
			m.mu.Unlock()
			return fmt.Errorf("failed to open mssql connection: %w", err)
		}
		m.db = db
	}
	db := m.db
	m.mu.Unlock()

	if err := db.PingContext(ctx); err != nil {
		m.mu.Lock()
		if m.db == db {
			db.Close()
			m.db = nil
		}
		m.mu.Unlock()
		return err
	}

	if !checkCDC {
		return nil
	}

	if m.autoEnableCDC {
		m.mu.Lock()
		needsCheck := len(m.captures) == 0
		m.mu.Unlock()

		if needsCheck {
			if err := m.ensureDatabaseCDC(ctx); err != nil {
				return err
			}
			for _, table := range m.tables {
				if _, err := m.ensureTableCDC(ctx, table); err != nil {
					return err
				}
			}
		}
		return nil
	}

	// Also check if CDC is enabled on the database
	var isCDCEnabled bool
	err := db.QueryRowContext(ctx, queryCheckDatabaseCDC).Scan(&isCDCEnabled)
	if err != nil {
		return fmt.Errorf("failed to check database CDC status: %w", err)
	}
	if !isCDCEnabled {
		if !m.autoEnableCDC {
			return fmt.Errorf("CDC is not enabled on database. Please enable it using 'sys.sp_cdc_enable_db' or set auto_enable_cdc to true")
		}
		if err := m.ensureDatabaseCDC(ctx); err != nil {
			return fmt.Errorf("failed to auto-enable CDC on database: %w (ensure user has db_owner role)", err)
		}
	}

	// If we have specific tables, check if CDC is enabled for them
	if len(m.tables) > 0 {
		for _, table := range m.tables {
			var isTableCDCEnabled int
			info, err := m.resolveTable(ctx, table)
			if err != nil {
				return fmt.Errorf("failed to resolve table '%s': %w", table, err)
			}

			err = db.QueryRowContext(ctx, queryCheckTableCDC, info.objectID).Scan(&isTableCDCEnabled)
			if err != nil {
				if err == sql.ErrNoRows {
					return fmt.Errorf("CDC is not enabled for table '%s'. Please enable it or set auto_enable_cdc to true", table)
				}
				return fmt.Errorf("failed to check CDC status for table '%s': %w", table, err)
			}
			if isTableCDCEnabled != 1 {
				if m.autoEnableCDC {
					if _, err := m.ensureTableCDC(ctx, table); err != nil {
						return fmt.Errorf("failed to auto-enable CDC for table '%s': %w (ensure user has db_owner role)", table, err)
					}
				} else {
					return fmt.Errorf("CDC is not enabled for table '%s' (is_tracked_by_cdc = 0)", table)
				}
			}
		}
	}

	return nil
}

func (m *MSSQLSource) Close() error {
	m.log("INFO", "Closing MSSQLSource", "tables", m.tables)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.db != nil {
		err := m.db.Close()
		m.db = nil
		return err
	}
	return nil
}

func (m *MSSQLSource) DiscoverDatabases(ctx context.Context) ([]string, error) {
	if err := m.ping(ctx, false); err != nil {
		return nil, err
	}

	m.mu.Lock()
	db := m.db
	m.mu.Unlock()
	if db == nil {
		return nil, fmt.Errorf("database connection not initialized")
	}

	rows, err := db.QueryContext(ctx, queryDiscoverDatabases)
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

func (m *MSSQLSource) DiscoverTables(ctx context.Context) ([]string, error) {
	if err := m.ping(ctx, false); err != nil {
		return nil, err
	}

	m.mu.Lock()
	db := m.db
	m.mu.Unlock()
	if db == nil {
		return nil, fmt.Errorf("database connection not initialized")
	}

	rows, err := db.QueryContext(ctx, queryDiscoverTables)
	if err != nil {
		return nil, fmt.Errorf("failed to query tables: %w", err)
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

func (m *MSSQLSource) Sample(ctx context.Context, table string) (hermod.Message, error) {
	if err := m.ping(ctx, false); err != nil {
		return nil, err
	}

	m.mu.Lock()
	db := m.db
	m.mu.Unlock()
	if db == nil {
		return nil, fmt.Errorf("database connection not initialized")
	}

	rows, err := db.QueryContext(ctx, fmt.Sprintf("SELECT TOP 1 * FROM %s", table))
	if err != nil {
		return nil, fmt.Errorf("failed to query sample record: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		return nil, fmt.Errorf("no records found in table %s", table)
	}

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	columns := make([]interface{}, len(cols))
	columnPointers := make([]interface{}, len(cols))
	for i := range columns {
		columnPointers[i] = &columns[i]
	}

	if err := rows.Scan(columnPointers...); err != nil {
		return nil, err
	}

	record := make(map[string]interface{})
	for i, colName := range cols {
		val := columns[i]
		if b, ok := val.([]byte); ok {
			record[colName] = string(b)
		} else {
			record[colName] = val
		}
	}

	afterJSON, _ := json.Marshal(message.SanitizeMap(record))

	msg := message.AcquireMessage()
	msg.SetID(fmt.Sprintf("sample-%s-%d", table, time.Now().Unix()))
	msg.SetOperation(hermod.OpSnapshot)
	msg.SetTable(table)
	msg.SetAfter(afterJSON)
	msg.SetMetadata("source", "mssql")
	msg.SetMetadata("sample", "true")

	return msg, nil
}
