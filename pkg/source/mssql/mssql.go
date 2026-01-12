package mssql

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
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

	mu       sync.Mutex
	lastLSN  []byte
	buffer   []hermod.Message
	captures map[string]string // table name -> capture instance name
}

func NewMSSQLSource(connString string, tables []string, autoEnableCDC bool) *MSSQLSource {
	normalizedTables := make([]string, len(tables))
	for i, t := range tables {
		normalizedTables[i] = normalizeTableName(t)
	}

	return &MSSQLSource{
		connString:    connString,
		tables:        normalizedTables,
		pollInterval:  5 * time.Second,
		autoEnableCDC: autoEnableCDC,
		captures:      make(map[string]string),
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
	var info tableInfo
	// OBJECT_ID is collation-aware and handles 1, 2, or 3 part names.
	err := m.db.QueryRowContext(ctx, queryResolveTableByID, table).Scan(&info.objectID, &info.schema, &info.name)

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
		rows, err = m.db.QueryContext(ctx, queryResolveTableByFullQualifiedName, schemaName, tableName)
	} else {
		rows, err = m.db.QueryContext(ctx, queryResolveTableByNameOnly, tableName)
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
	for {
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
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(m.pollInterval):
				continue
			}
		}
		m.mu.Unlock()
	}
}

func (m *MSSQLSource) poll(ctx context.Context) error {
	if err := m.Ping(ctx); err != nil {
		return err
	}

	if len(m.captures) == 0 {
		if err := m.discoverCaptures(ctx); err != nil {
			return err
		}
	}

	var maxLSN []byte
	err := m.db.QueryRowContext(ctx, queryGetMaxLSN).Scan(&maxLSN)
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
	configToID, resolveErrors, err := m.resolveTables(ctx)
	if err != nil {
		return err
	}

	rows, err := m.db.QueryContext(ctx, queryDiscoverCaptures)
	if err != nil {
		return fmt.Errorf("failed to discover capture instances: %w", err)
	}
	defer rows.Close()

	return m.assignCaptures(rows, configToID, resolveErrors)
}

func (m *MSSQLSource) resolveTables(ctx context.Context) (map[string]int32, map[string]error, error) {
	configToID := make(map[string]int32)
	resolveErrors := make(map[string]error)
	for _, t := range m.tables {
		info, err := m.resolveTable(ctx, t)
		if err == nil {
			configToID[t] = info.objectID
		} else {
			resolveErrors[t] = err
		}
	}

	if m.autoEnableCDC {
		if err := m.ensureDatabaseCDC(ctx); err != nil {
			return nil, nil, err
		}
		for _, t := range m.tables {
			info, err := m.ensureTableCDC(ctx, t)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to auto-enable CDC for table %s: %w", t, err)
			}
			configToID[t] = info.objectID
			delete(resolveErrors, t) // Clear error if it was resolved
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
	var isCDCEnabled bool
	err := m.db.QueryRowContext(ctx, queryCheckDatabaseCDC).Scan(&isCDCEnabled)
	if err != nil {
		return fmt.Errorf("failed to check if CDC is enabled on database: %w", err)
	}

	if !isCDCEnabled {
		_, err = m.db.ExecContext(ctx, queryEnableDatabaseCDC)
		if err != nil {
			return fmt.Errorf("failed to enable CDC on database: %w", err)
		}
		fmt.Println("Enabled CDC on database")
	}
	return nil
}

func (m *MSSQLSource) ensureTableCDC(ctx context.Context, table string) (*tableInfo, error) {
	if err := m.ensureDatabaseCDC(ctx); err != nil {
		return nil, err
	}

	info, err := m.resolveTable(ctx, table)
	if err != nil {
		return nil, err
	}

	// Check if CDC is already enabled for the table and has a capture instance.
	// We check both sys.tables and cdc.change_tables to be sure.
	var isTracked bool
	err = m.db.QueryRowContext(ctx, queryCheckTableCDC, info.objectID).Scan(&isTracked)

	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("failed to check if CDC is enabled on table %s.%s: %w", info.schema, info.name, err)
	}

	if err == sql.ErrNoRows || !isTracked {
		// Use named parameters for clarity and set supports_net_changes to 0 for better compatibility
		_, err = m.db.ExecContext(ctx, queryEnableTableCDC, info.schema, info.name)
		if err != nil {
			return nil, fmt.Errorf("failed to enable CDC on table %s.%s: %w", info.schema, info.name, err)
		}
		fmt.Printf("Enabled CDC on table %s.%s\n", info.schema, info.name)
	}

	return info, nil
}

func (m *MSSQLSource) pollTable(ctx context.Context, table, capture string, maxLSN []byte) ([]hermod.Message, error) {
	var minLSN []byte
	err := m.db.QueryRowContext(ctx, queryGetMinLSN, capture).Scan(&minLSN)
	if err != nil {
		return nil, fmt.Errorf("failed to get min lsn for %s: %w", capture, err)
	}

	m.mu.Lock()
	fromLSN := m.lastLSN
	m.mu.Unlock()

	if fromLSN == nil {
		fromLSN = minLSN
	} else {
		err = m.db.QueryRowContext(ctx, queryIncrementLSN, fromLSN).Scan(&fromLSN)
		if err != nil {
			return nil, fmt.Errorf("failed to increment lsn: %w", err)
		}
	}

	if bytes.Compare(fromLSN, minLSN) < 0 {
		fromLSN = minLSN
	}

	if bytes.Compare(fromLSN, maxLSN) > 0 {
		return nil, nil
	}

	query := fmt.Sprintf(queryGetTableChangesFormat, capture)
	rows, err := m.db.QueryContext(ctx, query, fromLSN, maxLSN)
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
		var operation int
		for i, colName := range columns {
			val := values[i]
			if b, ok := val.([]byte); ok && colName == "__$start_lsn" {
				rowLSN = b
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

		msgs = append(msgs, m.mapToMessage(table, operation, rowLSN, entry))
	}

	return msgs, nil
}

func (m *MSSQLSource) mapToMessage(table string, op int, lsn []byte, data map[string]interface{}) hermod.Message {
	msg := message.AcquireMessage()

	schema, tableName := parseTableParts(table)
	msg.SetSchema(schema)
	msg.SetTable(tableName)

	lsnHex := hex.EncodeToString(lsn)
	msg.SetID(lsnHex)
	msg.SetMetadata("lsn", lsnHex)
	msg.SetMetadata("source", "mssql")

	jsonBytes, _ := json.Marshal(data)
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

func (m *MSSQLSource) Ack(ctx context.Context, msg hermod.Message) error {
	lsnHex := msg.Metadata()["lsn"]
	if lsnHex == "" {
		return nil
	}

	lsn, err := hex.DecodeString(lsnHex)
	if err != nil {
		return fmt.Errorf("failed to parse LSN: %w", err)
	}

	m.mu.Lock()
	m.lastLSN = lsn
	m.mu.Unlock()
	return nil
}

func (m *MSSQLSource) Ping(ctx context.Context) error {
	if m.db == nil {
		db, err := sql.Open("sqlserver", m.connString)
		if err != nil {
			return fmt.Errorf("failed to open mssql connection: %w", err)
		}
		m.db = db
	}
	return m.db.PingContext(ctx)
}

func (m *MSSQLSource) Close() error {
	fmt.Println("Closing MSSQLSource")
	if m.db != nil {
		return m.db.Close()
	}
	return nil
}

func (m *MSSQLSource) DiscoverDatabases(ctx context.Context) ([]string, error) {
	if err := m.Ping(ctx); err != nil {
		return nil, err
	}

	rows, err := m.db.QueryContext(ctx, queryDiscoverDatabases)
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
	if err := m.Ping(ctx); err != nil {
		return nil, err
	}

	rows, err := m.db.QueryContext(ctx, queryDiscoverTables)
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
