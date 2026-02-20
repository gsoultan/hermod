package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/go-mysql-org/go-mysql/canal"
	mysql_driver "github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
	"github.com/user/hermod/pkg/sqlutil"
)

// MySQLSource implements the hermod.Source interface for MySQL CDC.
type MySQLSource struct {
	connString string
	useCDC     bool
	db         *sql.DB
	canal      *canal.Canal
	msgChan    chan hermod.Message
	errChan    chan error
	mu         sync.Mutex
	logger     hermod.Logger
}

func NewMySQLSource(connString string, useCDC bool) *MySQLSource {
	return &MySQLSource{
		connString: connString,
		useCDC:     useCDC,
		msgChan:    make(chan hermod.Message, 1000),
		errChan:    make(chan error, 10),
	}
}

func (m *MySQLSource) SetLogger(logger hermod.Logger) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logger = logger
}

func (m *MySQLSource) log(level, msg string, keysAndValues ...any) {
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

func (m *MySQLSource) init(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.db != nil && (!m.useCDC || m.canal != nil) {
		return nil
	}

	if m.db == nil {
		db, err := sql.Open("mysql", m.connString)
		if err != nil {
			return fmt.Errorf("failed to connect to mysql: %w", err)
		}
		m.db = db
		if err := m.db.PingContext(ctx); err != nil {
			return err
		}
	}

	if m.useCDC && m.canal == nil {
		cfg, err := mysql_driver.ParseDSN(m.connString)
		if err != nil {
			return fmt.Errorf("failed to parse mysql dsn: %w", err)
		}

		if _, _, err := net.SplitHostPort(cfg.Addr); err != nil {
			// addr might be just host
		}

		canalCfg := canal.NewDefaultConfig()
		canalCfg.Addr = cfg.Addr
		canalCfg.User = cfg.User
		canalCfg.Password = cfg.Passwd
		canalCfg.Dump.ExecutionPath = "" // Disable mysqldump for now

		c, err := canal.NewCanal(canalCfg)
		if err != nil {
			return fmt.Errorf("failed to create canal: %w", err)
		}

		c.SetEventHandler(&mysqlEventHandler{source: m})
		m.canal = c

		go func() {
			if err := m.canal.Run(); err != nil {
				m.log("ERROR", "canal run failed", "error", err)
				m.errChan <- err
			}
		}()
	}

	return nil
}

type mysqlEventHandler struct {
	canal.DummyEventHandler
	source *MySQLSource
}

func (h *mysqlEventHandler) OnRow(e *canal.RowsEvent) error {
	action := e.Action
	var rows [][]any
	if action == canal.UpdateAction {
		// For update, e.Rows contains [before, after, before, after, ...]
		for i := 1; i < len(e.Rows); i += 2 {
			rows = append(rows, e.Rows[i])
		}
	} else {
		rows = e.Rows
	}

	for _, row := range rows {
		msg := message.AcquireMessage()
		data := make(map[string]any)
		for i, col := range e.Table.Columns {
			val := row[i]
			// Handle []byte values from go-mysql
			if b, ok := val.([]byte); ok {
				val = string(b)
			}
			data[col.Name] = val
		}

		msg.SetData("_action", action)
		msg.SetData("_table", e.Table.Name)
		msg.SetData("_schema", e.Table.Schema)
		for k, v := range data {
			msg.SetData(k, v)
		}

		// Set a stable ID if possible (e.g. from PK)
		if len(e.Table.PKColumns) > 0 {
			pkVal := row[e.Table.PKColumns[0]]
			msg.SetID(fmt.Sprintf("%s:%s:%v", e.Table.Schema, e.Table.Name, pkVal))
		}

		select {
		case h.source.msgChan <- msg:
		default:
			// Buffer full
			message.ReleaseMessage(msg)
		}
	}
	return nil
}

func (h *mysqlEventHandler) String() string {
	return "mysqlEventHandler"
}

func (m *MySQLSource) Read(ctx context.Context) (hermod.Message, error) {
	if m.db == nil || (m.useCDC && m.canal == nil) {
		if err := m.init(ctx); err != nil {
			return nil, err
		}
	}

	if !m.useCDC {
		<-ctx.Done()
		return nil, ctx.Err()
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case msg := <-m.msgChan:
		return msg, nil
	case err := <-m.errChan:
		return nil, err
	}
}

func (m *MySQLSource) Ack(ctx context.Context, msg hermod.Message) error {
	return nil
}

func (m *MySQLSource) IsReady(ctx context.Context) error {
	if err := m.Ping(ctx); err != nil {
		return fmt.Errorf("mysql connection failed: %w", err)
	}

	if !m.useCDC {
		return nil
	}

	m.mu.Lock()
	db := m.db
	m.mu.Unlock()

	var err error
	if db == nil {
		db, err = sql.Open("mysql", m.connString)
		if err != nil {
			return fmt.Errorf("failed to open mysql connection for readiness check: %w", err)
		}
		defer db.Close()
	}

	// Check for binlog_format = ROW
	var binlogFormat string
	err = db.QueryRowContext(ctx, "SHOW VARIABLES LIKE 'binlog_format'").Scan(&err, &binlogFormat)
	if err != nil {
		return fmt.Errorf("failed to check mysql 'binlog_format': %w", err)
	}
	if binlogFormat != "ROW" {
		return fmt.Errorf("mysql 'binlog_format' must be 'ROW' for CDC (currently '%s'). Run 'SET GLOBAL binlog_format = ROW'", binlogFormat)
	}

	// Check for log_bin = ON
	var logBin string
	err = db.QueryRowContext(ctx, "SHOW VARIABLES LIKE 'log_bin'").Scan(&err, &logBin)
	if err != nil {
		return fmt.Errorf("failed to check mysql 'log_bin': %w", err)
	}
	if logBin != "ON" {
		return fmt.Errorf("mysql 'log_bin' must be 'ON' for CDC. Please enable binary logging in your MySQL configuration")
	}

	// Check for binlog_row_image = FULL (recommended)
	var binlogRowImage string
	err = db.QueryRowContext(ctx, "SHOW VARIABLES LIKE 'binlog_row_image'").Scan(&err, &binlogRowImage)
	if err == nil && binlogRowImage != "FULL" {
		m.log("WARN", "mysql 'binlog_row_image' is not 'FULL' (currently '%s'). It is recommended to set it to 'FULL' to ensure all column values are present in events", binlogRowImage)
	}

	return nil
}

func (m *MySQLSource) Ping(ctx context.Context) error {
	m.mu.Lock()
	db := m.db
	m.mu.Unlock()

	if db == nil {
		// Just connect and ping, don't trigger anything else if m.init was heavier
		// In this case m.init is already light, but we should be consistent
		db, err := sql.Open("mysql", m.connString)
		if err != nil {
			return fmt.Errorf("failed to open mysql connection for ping: %w", err)
		}
		defer db.Close()
		return db.PingContext(ctx)
	}
	return db.PingContext(ctx)
}

func (m *MySQLSource) Close() error {
	m.log("INFO", "Closing MySQLSource")
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.canal != nil {
		m.canal.Close()
	}

	if m.db != nil {
		err := m.db.Close()
		m.db = nil
		return err
	}
	return nil
}

func (m *MySQLSource) DiscoverDatabases(ctx context.Context) ([]string, error) {
	if m.db == nil {
		if err := m.init(ctx); err != nil {
			return nil, err
		}
	}

	rows, err := m.db.QueryContext(ctx, "SHOW DATABASES")
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

func (m *MySQLSource) DiscoverTables(ctx context.Context) ([]string, error) {
	if m.db == nil {
		if err := m.init(ctx); err != nil {
			return nil, err
		}
	}

	rows, err := m.db.QueryContext(ctx, "SHOW TABLES")
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

func (m *MySQLSource) DiscoverColumns(ctx context.Context, table string) ([]hermod.ColumnInfo, error) {
	if m.db == nil {
		if err := m.init(ctx); err != nil {
			return nil, err
		}
	}

	query := `
		SELECT COLUMN_NAME, DATA_TYPE, COALESCE(IS_NULLABLE = 'YES', 0), 
		       COALESCE(COLUMN_KEY = 'PRI', 0), COALESCE(EXTRA = 'auto_increment', 0), COLUMN_DEFAULT 
		FROM INFORMATION_SCHEMA.COLUMNS 
		WHERE TABLE_NAME = ? AND TABLE_SCHEMA = DATABASE() 
		ORDER BY ORDINAL_POSITION`

	rows, err := m.db.QueryContext(ctx, query, table)
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

func (m *MySQLSource) Sample(ctx context.Context, table string) (hermod.Message, error) {
	if m.db == nil {
		if err := m.init(ctx); err != nil {
			return nil, err
		}
	}

	quoted, err := sqlutil.QuoteIdent("mysql", table)
	if err != nil {
		return nil, fmt.Errorf("invalid table name: %w", err)
	}
	rows, err := m.db.QueryContext(ctx, fmt.Sprintf("SELECT * FROM %s LIMIT 1", quoted))
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

	columns := make([]any, len(cols))
	columnPointers := make([]any, len(cols))
	for i := range columns {
		columnPointers[i] = &columns[i]
	}

	if err := rows.Scan(columnPointers...); err != nil {
		return nil, err
	}

	record := make(map[string]any)
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
	msg.SetMetadata("source", "mysql")
	msg.SetMetadata("sample", "true")

	return msg, nil
}

func (m *MySQLSource) Snapshot(ctx context.Context, tables ...string) error {
	if err := m.init(ctx); err != nil {
		return err
	}

	targetTables := tables
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

func (m *MySQLSource) snapshotTable(ctx context.Context, table string) error {
	quoted, err := sqlutil.QuoteIdent("mysql", table)
	if err != nil {
		return fmt.Errorf("invalid table name %q: %w", table, err)
	}

	rows, err := m.db.QueryContext(ctx, fmt.Sprintf("SELECT * FROM %s", quoted))
	if err != nil {
		return fmt.Errorf("failed to query table %q: %w", table, err)
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return err
	}

	for rows.Next() {
		values := make([]any, len(columns))
		valuePtrs := make([]any, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			return err
		}

		record := make(map[string]any)
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
		msg.SetID(fmt.Sprintf("snapshot-%s-%d-%s", table, time.Now().UnixNano(), uuid.New().String()))
		msg.SetOperation(hermod.OpSnapshot)
		msg.SetTable(table)
		msg.SetAfter(afterJSON)
		msg.SetMetadata("source", "mysql")
		msg.SetMetadata("snapshot", "true")

		select {
		case m.msgChan <- msg:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return rows.Err()
}
