package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-mysql-org/go-mysql/canal"
	"github.com/go-mysql-org/go-mysql/mysql"
	"github.com/go-mysql-org/go-mysql/replication"
	mysql_driver "github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/comm/message"
	sourcebuf "github.com/user/hermod/pkg/comm/source"
	"github.com/user/hermod/pkg/infra/sqlutil"
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

	// tables scopes the initial load. Empty means every table in the
	// connected database, which is what DiscoverTables reports.
	tables []string

	// ackedPos is the binlog position of the last acknowledged message, and
	// the only position GetState reports. The engine persists that on every ack
	// (registry_routing.go, statefulSource.Ack), so it decides where a restart
	// comes back to: anything ahead of it is a change the pipeline will never
	// be given again.
	//
	// Before this existed the source implemented none of hermod.Stateful, so
	// canal was started with a zero Position. That is not "start from now": an
	// empty binlog filename makes the server serve the oldest file it still
	// holds, so every start replayed the whole retained history and no restart
	// could resume from what had been delivered.
	ackedPos mysql.Position
	// currentFile is the binlog the reader is on, tracked through OnRotate.
	// The row events themselves carry only an offset (Header.LogPos), which is
	// meaningless without the file it is an offset into.
	currentFile string

	initialLoad bool
	// initialLoadComplete is the source's record of having backfilled. MySQL
	// leaves nothing server-side that could stand in for it the way a
	// PostgreSQL replication slot does, and the binlog position cannot: a
	// backfill moves no binlog position, so a table that was carried across
	// and then never written to would look exactly like one that had never run.
	initialLoadComplete bool
	initialLoadStarted  bool
	backfillCancel      context.CancelFunc
}

// SetTables scopes the initial load to the named tables. Without it the
// backfill covers every table DiscoverTables reports.
func (m *MySQLSource) SetTables(tables ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tables = append([]string(nil), tables...)
}

// SetInitialLoad asks for a one-time backfill of the watched tables before the
// binlog is read.
//
// It runs only when the source has no record of having run before, so enabling
// it on a workflow that is already streaming does nothing until that record is
// cleared. See initialLoadComplete for what the record is.
func (m *MySQLSource) SetInitialLoad(enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.initialLoad = enabled
}

func NewMySQLSource(connString string, useCDC bool) *MySQLSource {
	return &MySQLSource{
		connString: connString,
		useCDC:     useCDC,
		msgChan:    make(chan hermod.Message, sourcebuf.DefaultSourceBuffer),
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
	if m.db != nil && (!m.useCDC || m.canal != nil) {
		m.mu.Unlock()
		return nil
	}
	db := m.db
	m.mu.Unlock()

	if db == nil {
		newDb, err := sql.Open("mysql", m.connString)
		if err != nil {
			return fmt.Errorf("failed to connect to mysql: %w", err)
		}
		if err := newDb.PingContext(ctx); err != nil {
			newDb.Close()
			return err
		}
		m.mu.Lock()
		if m.db == nil {
			m.db = newDb
		} else {
			// Another goroutine won the race; close the handle we opened rather
			// than leaking it.
			newDb.Close()
		}
		m.mu.Unlock()
	}

	m.mu.Lock()
	defer m.mu.Unlock()
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

		// Where to start, in order of preference. Never the zero Position:
		// canal passes it straight through as an empty binlog filename, which
		// the server answers with the oldest file it still holds.
		start := m.ackedPos
		resumed := start.Name != ""
		if !resumed {
			pos, err := c.GetMasterPos()
			if err != nil {
				return fmt.Errorf("failed to read the current binlog position, which is where "+
					"a first run has to start: %w", err)
			}
			start = pos

			// Record it as the resume floor straight away, rather than waiting
			// for the first acknowledged change. Everything strictly before
			// this point has been handled — either carried across by the
			// backfill about to run, or deliberately skipped because a source
			// with no state starts from now — so it is a true watermark, and
			// it is the only one that exists until a change is acknowledged.
			//
			// Without it there is a window with no persisted position at all:
			// a source that backfilled a quiet table and was then restarted
			// would call GetMasterPos again and silently skip everything
			// written while it was down.
			m.ackedPos = start
		}
		m.currentFile = start.Name

		wantBackfill := m.initialLoad && !m.initialLoadStarted && !m.initialLoadComplete && !resumed
		if wantBackfill {
			m.initialLoadStarted = true
		}
		backfillCtx, cancel := context.WithCancel(context.Background())
		m.backfillCancel = cancel

		go func() {
			// The backfill runs before the binlog reader and on the same
			// goroutine, so the rows already in the tables reach the pipeline
			// ahead of any change to them. The position was taken before the
			// backfill began, so a write made while it runs is at or after
			// `start` and is replayed by the reader rather than lost between
			// the two. Where the two overlap the row arrives twice, which
			// sink-side idempotency collapses.
			//
			// It cannot run inline here: it delivers into the same channel
			// Read drains, so a table larger than the buffer would block until
			// Read emptied it, and Read is what this goroutine is feeding.
			if wantBackfill {
				m.runInitialLoad(backfillCtx)
			}
			if backfillCtx.Err() != nil {
				return
			}
			m.log("INFO", "Starting binlog reader", "file", start.Name, "position", start.Pos,
				"resumed", resumed)
			// c, not m.canal: the field is read from other goroutines and there
			// is no reason to reach for it when the value is already in hand.
			if err := c.RunFrom(start); err != nil {
				m.log("ERROR", "canal run failed", "error", err)
				m.errChan <- err
			}
		}()
	}

	return nil
}

// runInitialLoad carries across the rows already in the watched tables.
//
// A failure is logged rather than returned: the binlog position is already
// pinned by this point and the reader is about to start from it, so refusing to
// stream would strand a working reader to no purpose. What is lost is the
// pre-existing rows, which is what the log says. The completion record is not
// written in that case, so the next restart tries again rather than quietly
// settling for a partial copy.
func (m *MySQLSource) runInitialLoad(ctx context.Context) {
	m.mu.Lock()
	tables := append([]string(nil), m.tables...)
	m.mu.Unlock()

	if len(tables) == 0 {
		discovered, err := m.DiscoverTables(ctx)
		if err != nil {
			m.log("ERROR", "Initial load could not discover tables; rows already in the "+
				"source will not be carried across", "error", err.Error())
			return
		}
		tables = discovered
	}

	m.log("INFO", "Initial load starting", "tables", strings.Join(tables, ","))
	failed := 0
	for _, table := range tables {
		if ctx.Err() != nil {
			return
		}
		if err := m.snapshotTable(ctx, table); err != nil {
			failed++
			m.log("ERROR", "Initial load failed for a table; its existing rows were not "+
				"carried across, though later changes to it will still stream",
				"table", table, "error", err.Error())
		}
	}
	if failed > 0 {
		m.log("WARN", "Initial load finished with failures; it will run again on the next "+
			"restart because no completion was recorded", "tables_failed", failed)
		return
	}

	m.mu.Lock()
	m.initialLoadComplete = true
	m.mu.Unlock()
	m.log("INFO", "Initial load complete; reading the binlog from the pinned position")
}

type mysqlEventHandler struct {
	canal.DummyEventHandler
	source *MySQLSource
}

// mysqlActionToOperation maps a binlog action onto Hermod's vocabulary. A sink
// distinguishes an insert from a delete by this alone, so an unrecognised
// action must not quietly become a create.
func mysqlActionToOperation(action string) hermod.Operation {
	switch action {
	case canal.InsertAction:
		return hermod.OpCreate
	case canal.UpdateAction:
		return hermod.OpUpdate
	case canal.DeleteAction:
		return hermod.OpDelete
	default:
		return hermod.Operation(action)
	}
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

		// The fields the rest of the pipeline actually routes on.
		//
		// This path set _table and _schema as *data* and stopped there, so
		// Table(), Schema(), Operation() and After() were all empty on every
		// MySQL CDC message. A SQL sink takes its target table from the message
		// when its own config does not pin one, and decides insert against
		// delete from the operation, so binlog changes arrived at sinks with no
		// table, no operation and no after-image. The snapshot path a few
		// hundred lines below always set them; only CDC did not.
		msg.SetTable(e.Table.Name)
		msg.SetSchema(e.Table.Schema)
		msg.SetOperation(mysqlActionToOperation(action))
		// Every other source stamps this, and the snapshot path below does too;
		// only CDC did not, so a routing or audit rule keyed on it saw nothing.
		msg.SetMetadata("source", "mysql")

		// Where this row came from, so acknowledging it can move the resume
		// position. Header.LogPos is the offset *after* the event, which is
		// exactly where a restart should pick up.
		if e.Header != nil {
			h.source.mu.Lock()
			file := h.source.currentFile
			h.source.mu.Unlock()
			if file != "" {
				msg.SetMetadata("binlog_file", file)
				msg.SetMetadata("binlog_pos", strconv.FormatUint(uint64(e.Header.LogPos), 10))
			}
		}
		if afterJSON, err := json.Marshal(data); err == nil {
			msg.SetAfter(afterJSON)
		} else {
			h.source.log("ERROR", "cannot encode row image",
				"schema", e.Table.Schema, "table", e.Table.Name, "error", err)
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

// OnRotate records the binlog file the reader has moved onto. Row events carry
// only an offset, so without this the acknowledged position would name the
// wrong file the moment the server rotated and a restart would resume at an
// arbitrary point in it.
func (h *mysqlEventHandler) OnRotate(header *replication.EventHeader, e *replication.RotateEvent) error {
	h.source.mu.Lock()
	h.source.currentFile = string(e.NextLogName)
	h.source.mu.Unlock()
	return nil
}

func (h *mysqlEventHandler) String() string {
	return "mysqlEventHandler"
}

func (m *MySQLSource) Read(ctx context.Context) (hermod.Message, error) {
	// Under the lock: Close clears both of these from another goroutine, and a
	// reader is normally still in flight when it does. The race detector never
	// saw it because the only test that drives this path is integration-tagged
	// and CI runs that job without -race.
	m.mu.Lock()
	needInit := m.db == nil || (m.useCDC && m.canal == nil)
	m.mu.Unlock()
	if needInit {
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

// Ack moves the position a restart resumes from.
//
// Snapshot messages carry no binlog position and move nothing — they did not
// come from the binlog. What records a finished backfill is initialLoadComplete,
// which GetState reports separately.
//
// The position only ever moves forward, matching the PostgreSQL source's
// handling of the LSN: out-of-order acks cannot drag it backwards.
func (m *MySQLSource) Ack(ctx context.Context, msg hermod.Message) error {
	if msg == nil {
		return nil
	}
	meta := msg.Metadata()
	file := meta["binlog_file"]
	if file == "" {
		return nil
	}
	pos, err := strconv.ParseUint(meta["binlog_pos"], 10, 32)
	if err != nil {
		// Acking must still succeed; the previously stored position stands.
		return nil
	}

	acked := mysql.Position{Name: file, Pos: uint32(pos)}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ackedPos.Name == "" || acked.Compare(m.ackedPos) > 0 {
		m.ackedPos = acked
	}
	return nil
}

func (m *MySQLSource) GetState() map[string]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ackedPos.Name == "" && !m.initialLoadComplete {
		return nil
	}
	state := make(map[string]string, 3)
	if m.ackedPos.Name != "" {
		state["binlog_file"] = m.ackedPos.Name
		state["binlog_pos"] = strconv.FormatUint(uint64(m.ackedPos.Pos), 10)
	}
	// Reported separately from the position because it has to survive the case
	// where there is none: a backfill moves no binlog position, so without this
	// a table that was carried across and then never written to would be
	// backfilled again on every restart.
	if m.initialLoadComplete {
		state["initial_load"] = "done"
	}
	return state
}

func (m *MySQLSource) SetState(state map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if file := state["binlog_file"]; file != "" {
		if pos, err := strconv.ParseUint(state["binlog_pos"], 10, 32); err == nil {
			m.ackedPos = mysql.Position{Name: file, Pos: uint32(pos)}
		}
	}
	if state["initial_load"] == "done" {
		m.initialLoadComplete = true
	}
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
		return errors.New("mysql 'log_bin' must be 'ON' for CDC. Please enable binary logging in your MySQL configuration")
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

	// Stop the backfill before the handle it reads through is closed, so a
	// cancelled source does not leave a goroutine scanning tables.
	if m.backfillCancel != nil {
		m.backfillCancel()
		m.backfillCancel = nil
	}

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
	m.mu.Lock()
	db := m.db
	m.mu.Unlock()
	if db == nil {
		if err := m.init(ctx); err != nil {
			return nil, err
		}
		m.mu.Lock()
		db = m.db
		m.mu.Unlock()
	}

	rows, err := db.QueryContext(ctx, "SHOW DATABASES")
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return databases, nil
}

func (m *MySQLSource) DiscoverTables(ctx context.Context) ([]string, error) {
	m.mu.Lock()
	db := m.db
	m.mu.Unlock()
	if db == nil {
		if err := m.init(ctx); err != nil {
			return nil, err
		}
		m.mu.Lock()
		db = m.db
		m.mu.Unlock()
	}

	rows, err := db.QueryContext(ctx, "SHOW TABLES")
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return tables, nil
}

func (m *MySQLSource) DiscoverColumns(ctx context.Context, table string) ([]hermod.ColumnInfo, error) {
	m.mu.Lock()
	db := m.db
	m.mu.Unlock()
	if db == nil {
		if err := m.init(ctx); err != nil {
			return nil, err
		}
		m.mu.Lock()
		db = m.db
		m.mu.Unlock()
	}

	query := `
		SELECT COLUMN_NAME, DATA_TYPE, COALESCE(IS_NULLABLE = 'YES', 0), 
		       COALESCE(COLUMN_KEY = 'PRI', 0), COALESCE(EXTRA = 'auto_increment', 0), COLUMN_DEFAULT 
		FROM INFORMATION_SCHEMA.COLUMNS 
		WHERE TABLE_NAME = ? AND TABLE_SCHEMA = DATABASE() 
		ORDER BY ORDINAL_POSITION`

	rows, err := db.QueryContext(ctx, query, table)
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return columns, nil
}

func (m *MySQLSource) Sample(ctx context.Context, table string) (hermod.Message, error) {
	m.mu.Lock()
	db := m.db
	m.mu.Unlock()
	if db == nil {
		if err := m.init(ctx); err != nil {
			return nil, err
		}
		m.mu.Lock()
		db = m.db
		m.mu.Unlock()
	}

	quoted, err := sqlutil.QuoteIdent("mysql", table)
	if err != nil {
		return nil, fmt.Errorf("invalid table name: %w", err)
	}
	rows, err := db.QueryContext(ctx, fmt.Sprintf("SELECT * FROM %s LIMIT 1", quoted))
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

const snapshotBatchSize = 1000

func (m *MySQLSource) snapshotTable(ctx context.Context, table string) error {
	cols, err := m.DiscoverColumns(ctx, table)
	if err != nil {
		return fmt.Errorf("failed to discover columns for snapshot of %q: %w", table, err)
	}

	var colNames []string
	var pkCols []string
	for _, c := range cols {
		quoted, _ := sqlutil.QuoteIdent("mysql", c.Name)
		colNames = append(colNames, quoted)
		if c.IsPK {
			pkCols = append(pkCols, quoted)
		}
	}

	if len(colNames) == 0 {
		return fmt.Errorf("no columns found for table %q", table)
	}
	colList := strings.Join(colNames, ", ")

	quotedTable, err := sqlutil.QuoteIdent("mysql", table)
	if err != nil {
		return fmt.Errorf("invalid table name %q: %w", table, err)
	}

	orderBy := "1"
	if len(pkCols) > 0 {
		orderBy = strings.Join(pkCols, ", ")
	} else {
		orderBy = colNames[0]
	}

	// Taken under the lock. The backfill runs on its own goroutine now, and
	// Close clears the handle from another one; reading the field directly was
	// a race that only became reachable when the snapshot stopped being
	// synchronous.
	m.mu.Lock()
	db := m.db
	m.mu.Unlock()
	if db == nil {
		return errors.New("the source was closed before the table could be read")
	}

	offset := 0
	for {
		query := fmt.Sprintf("SELECT %s FROM %s ORDER BY %s LIMIT %d OFFSET %d",
			colList, quotedTable, orderBy, snapshotBatchSize, offset)

		rows, err := db.QueryContext(ctx, query)
		if err != nil {
			return fmt.Errorf("failed to query snapshot batch for %q at offset %d: %w", table, offset, err)
		}

		count, err := m.processSnapshotRows(ctx, rows, table, pkCols)
		rows.Close()
		if err != nil {
			return err
		}

		if count < snapshotBatchSize {
			break
		}
		offset += snapshotBatchSize
	}

	return nil
}

func (m *MySQLSource) processSnapshotRows(ctx context.Context, rows *sql.Rows, table string, pkCols []string) (int, error) {
	columns, err := rows.Columns()
	if err != nil {
		return 0, err
	}

	count := 0
	for rows.Next() {
		values := make([]any, len(columns))
		valuePtrs := make([]any, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			return count, err
		}

		record := make(map[string]any, len(columns))
		for i, colName := range columns {
			val := values[i]
			if b, ok := val.([]byte); ok {
				record[colName] = string(b)
			} else {
				record[colName] = val
			}
		}

		if err := m.emitSnapshotRecord(ctx, table, record, pkCols); err != nil {
			return count, err
		}
		count++
	}
	return count, rows.Err()
}

func (m *MySQLSource) emitSnapshotRecord(ctx context.Context, table string, record map[string]any, pkCols []string) error {
	afterJSON, _ := json.Marshal(message.SanitizeMap(record))

	msg := message.AcquireMessage()
	if len(pkCols) > 0 {
		var pkVals []string
		for _, pk := range pkCols {
			cleanPK := strings.Trim(pk, "` ")
			pkVals = append(pkVals, fmt.Sprintf("%v", record[cleanPK]))
		}
		msg.SetID(fmt.Sprintf("snapshot-%s-%s", table, strings.Join(pkVals, "-")))
	} else {
		msg.SetID(fmt.Sprintf("snapshot-%s-%d-%s", table, time.Now().UnixNano(), uuid.New().String()))
	}

	msg.SetOperation(hermod.OpSnapshot)
	msg.SetTable(table)
	msg.SetAfter(afterJSON)
	msg.SetMetadata("source", "mysql")
	msg.SetMetadata("snapshot", "true")

	select {
	case m.msgChan <- msg:
		return nil
	case <-ctx.Done():
		message.ReleaseMessage(msg)
		return ctx.Err()
	}
}

func (m *MySQLSource) ExecuteSQL(ctx context.Context, query string) ([]map[string]any, error) {
	if err := m.init(ctx); err != nil {
		return nil, err
	}
	rows, err := m.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return sqlutil.ScanRows(rows)
}
