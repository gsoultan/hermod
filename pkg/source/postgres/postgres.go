package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pglogrepl"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgproto3"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
	"github.com/user/hermod/pkg/sqlutil"
)

// PostgresSource implements the hermod.Source interface for PostgreSQL CDC.
type PostgresSource struct {
	connString      string
	slotName        string
	publicationName string
	tables          []string
	useCDC          bool
	persistentSlot  bool      // Whether to keep the slot on Close
	conn            *pgx.Conn // Standard connection for metadata
	replConn        *pgx.Conn // Replication connection for streaming
	typeMap         *pgtype.Map
	relations       map[uint32]*pglogrepl.RelationMessage
	mu              sync.Mutex
	initialized     bool
	lastReceivedLSN pglogrepl.LSN
	lastAckedLSN    pglogrepl.LSN
	msgChan         chan hermod.Message
	errChan         chan error
	cancel          context.CancelFunc
	wg              sync.WaitGroup
	logger          hermod.Logger
}

func NewPostgresSource(connString, slotName, publicationName string, tables []string, useCDC bool) *PostgresSource {
	return &PostgresSource{
		connString:      connString,
		slotName:        slotName,
		publicationName: publicationName,
		tables:          tables,
		useCDC:          useCDC,
		persistentSlot:  true, // Default to persistent for reliability
		relations:       make(map[uint32]*pglogrepl.RelationMessage),
		msgChan:         make(chan hermod.Message, 1000),
		errChan:         make(chan error, 10),
	}
}

func (p *PostgresSource) SetPersistentSlot(persistent bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.persistentSlot = persistent
}

func (p *PostgresSource) SetLogger(logger hermod.Logger) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.logger = logger
}

func (p *PostgresSource) log(level, msg string, keysAndValues ...interface{}) {
	p.mu.Lock()
	logger := p.logger
	p.mu.Unlock()

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

func (p *PostgresSource) ensurePublication(ctx context.Context) error {
	quotedPub, err := sqlutil.QuoteIdent("postgres", p.publicationName)
	if err != nil {
		return fmt.Errorf("invalid publication name %q: %w", p.publicationName, err)
	}

	var exists bool
	err = p.conn.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_publication WHERE pubname = $1)", p.publicationName).Scan(&exists)
	if err != nil {
		return fmt.Errorf("failed to check if publication exists: %w", err)
	}

	if !exists {
		tablesClause := "ALL TABLES"
		if len(p.tables) > 0 {
			quotedTables := make([]string, len(p.tables))
			for i, t := range p.tables {
				qt, err := sqlutil.QuoteIdent("postgres", t)
				if err != nil {
					return fmt.Errorf("invalid table name %q: %w", t, err)
				}
				quotedTables[i] = qt
			}
			tablesClause = "TABLE " + strings.Join(quotedTables, ", ")
		}
		query := fmt.Sprintf("CREATE PUBLICATION %s FOR %s", quotedPub, tablesClause)
		_, err = p.conn.Exec(ctx, query)
		if err != nil {
			// Fallback for non-superuser if ALL TABLES failed
			if len(p.tables) == 0 && (strings.Contains(err.Error(), "superuser") || strings.Contains(err.Error(), "permission")) {
				p.log("WARN", "Failed to create publication FOR ALL TABLES (need superuser), falling back to listing all tables", "error", err)
				return p.createPublicationWithAllTables(ctx, quotedPub)
			}
			return fmt.Errorf("failed to create publication: %w", err)
		}
		p.log("INFO", "Created publication", "publication", p.publicationName)
		return nil
	}

	// Publication exists, ensure it has the correct tables
	var pubAllTables bool
	err = p.conn.QueryRow(ctx, "SELECT puballtables FROM pg_publication WHERE pubname = $1", p.publicationName).Scan(&pubAllTables)
	if err != nil {
		return fmt.Errorf("failed to check publication details: %w", err)
	}

	if len(p.tables) == 0 {
		if !pubAllTables {
			_, err = p.conn.Exec(ctx, fmt.Sprintf("ALTER PUBLICATION %s SET FOR ALL TABLES", quotedPub))
			if err != nil {
				if strings.Contains(err.Error(), "superuser") || strings.Contains(err.Error(), "permission") {
					p.log("WARN", "Failed to set publication to ALL TABLES (need superuser), falling back to listing all tables", "error", err)
					return p.setPublicationWithAllTables(ctx, quotedPub)
				}
				return fmt.Errorf("failed to set publication to ALL TABLES: %w", err)
			}
			p.log("INFO", "Updated publication to ALL TABLES", "publication", p.publicationName)
		}
		return nil
	}

	if pubAllTables {
		// Switch from ALL TABLES to specific tables
		quotedTables := make([]string, len(p.tables))
		for i, t := range p.tables {
			quotedTables[i], _ = sqlutil.QuoteIdent("postgres", t)
		}
		query := fmt.Sprintf("ALTER PUBLICATION %s SET TABLE %s", quotedPub, strings.Join(quotedTables, ", "))
		_, err = p.conn.Exec(ctx, query)
		if err != nil {
			return fmt.Errorf("failed to update publication from ALL TABLES to specific tables: %w", err)
		}
		p.log("INFO", "Updated publication from ALL TABLES to specific tables", "publication", p.publicationName)
		return nil
	}

	// Check if any tables are missing from the publication
	rows, err := p.conn.Query(ctx, "SELECT schemaname, tablename FROM pg_publication_tables WHERE pubname = $1", p.publicationName)
	if err != nil {
		return fmt.Errorf("failed to get publication tables: %w", err)
	}
	defer rows.Close()

	existingTables := make(map[string]bool)
	for rows.Next() {
		var schema, table string
		if err := rows.Scan(&schema, &table); err != nil {
			return fmt.Errorf("failed to scan publication table: %w", err)
		}
		existingTables[table] = true
		existingTables[schema+"."+table] = true
	}

	needsUpdate := false
	for _, t := range p.tables {
		if !existingTables[t] {
			needsUpdate = true
			break
		}
	}

	if needsUpdate {
		quotedTables := make([]string, len(p.tables))
		for i, t := range p.tables {
			quotedTables[i], _ = sqlutil.QuoteIdent("postgres", t)
		}
		query := fmt.Sprintf("ALTER PUBLICATION %s SET TABLE %s", quotedPub, strings.Join(quotedTables, ", "))
		_, err = p.conn.Exec(ctx, query)
		if err != nil {
			return fmt.Errorf("failed to update publication tables: %w", err)
		}
		p.log("INFO", "Updated publication tables", "publication", p.publicationName, "tables", strings.Join(p.tables, ", "))
	}

	return nil
}

func (p *PostgresSource) createPublicationWithAllTables(ctx context.Context, quotedPub string) error {
	allTables, err := p.DiscoverTables(ctx)
	if err != nil {
		return fmt.Errorf("failed to discover tables for publication fallback: %w", err)
	}
	if len(allTables) == 0 {
		return fmt.Errorf("no tables found in database")
	}
	quotedTables := make([]string, len(allTables))
	for i, t := range allTables {
		quotedTables[i], _ = sqlutil.QuoteIdent("postgres", t)
	}
	query := fmt.Sprintf("CREATE PUBLICATION %s FOR TABLE %s", quotedPub, strings.Join(quotedTables, ", "))
	_, err = p.conn.Exec(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to create publication with discovered tables: %w", err)
	}
	p.log("INFO", "Created publication with all discovered tables", "publication", p.publicationName)
	return nil
}

func (p *PostgresSource) setPublicationWithAllTables(ctx context.Context, quotedPub string) error {
	allTables, err := p.DiscoverTables(ctx)
	if err != nil {
		return fmt.Errorf("failed to discover tables for publication fallback: %w", err)
	}
	if len(allTables) == 0 {
		return fmt.Errorf("no tables found in database")
	}
	quotedTables := make([]string, len(allTables))
	for i, t := range allTables {
		quotedTables[i], _ = sqlutil.QuoteIdent("postgres", t)
	}
	query := fmt.Sprintf("ALTER PUBLICATION %s SET TABLE %s", quotedPub, strings.Join(quotedTables, ", "))
	_, err = p.conn.Exec(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to update publication with discovered tables: %w", err)
	}
	p.log("INFO", "Updated publication with all discovered tables", "publication", p.publicationName)
	return nil
}

func (p *PostgresSource) ensureReplicationSlot(ctx context.Context) error {
	var exists bool
	err := p.conn.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_replication_slots WHERE slot_name = $1)", p.slotName).Scan(&exists)
	if err != nil {
		return fmt.Errorf("failed to check if replication slot exists: %w", err)
	}

	if !exists {
		_, err = p.conn.Exec(ctx, "SELECT pg_create_logical_replication_slot($1, 'pgoutput')", p.slotName)
		if err != nil {
			if strings.Contains(err.Error(), "wal_level") {
				return fmt.Errorf("failed to create replication slot: wal_level must be set to 'logical' in postgres.conf: %w", err)
			}
			return fmt.Errorf("failed to create replication slot: %w", err)
		}
		p.log("INFO", "Created replication slot", "slot", p.slotName)
	}
	return nil
}

func (p *PostgresSource) Read(ctx context.Context) (hermod.Message, error) {
	if err := p.init(ctx); err != nil {
		return nil, err
	}

	if !p.useCDC {
		<-ctx.Done()
		return nil, ctx.Err()
	}

	for {
		select {
		case msg := <-p.msgChan:
			return msg, nil
		case err := <-p.errChan:
			return nil, err
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(1 * time.Second):
			// Check if we are still initialized
			p.mu.Lock()
			init := p.initialized
			p.mu.Unlock()
			if !init {
				if err := p.init(ctx); err != nil {
					return nil, err
				}
			}
			// Small timeout to allow checking context and re-init
			continue
		}
	}
}

func (p *PostgresSource) streamLoop(ctx context.Context) {
	defer p.wg.Done()
	defer func() {
		p.mu.Lock()
		p.initialized = false
		if p.replConn != nil {
			p.replConn.Close(context.Background())
			p.replConn = nil
		}
		p.mu.Unlock()
	}()
	p.log("INFO", "Starting streamLoop", "slot", p.slotName)

	for {
		p.mu.Lock()
		conn := p.replConn
		p.mu.Unlock()

		if conn == nil || conn.IsClosed() {
			return
		}

		msg, err := conn.PgConn().ReceiveMessage(ctx)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				p.log("ERROR", "Replication stream error", "slot", p.slotName, "error", err)
				select {
				case p.errChan <- err:
				case <-ctx.Done():
				}
			}
			return
		}

		switch m := msg.(type) {
		case *pgproto3.ErrorResponse:
			p.log("ERROR", "Postgres error response", "slot", p.slotName, "error", m.Message)
			select {
			case p.errChan <- fmt.Errorf("postgres error: %s", m.Message):
			case <-ctx.Done():
			}
			return
		case *pgproto3.CopyData:
			switch m.Data[0] {
			case 'k': // Primary Keepalive Message
				pka, err := pglogrepl.ParsePrimaryKeepaliveMessage(m.Data[1:])
				if err != nil {
					p.log("ERROR", "Failed to parse keepalive", "error", err)
					continue
				}

				p.mu.Lock()
				p.lastReceivedLSN = pka.ServerWALEnd
				if pka.ReplyRequested {
					err = pglogrepl.SendStandbyStatusUpdate(ctx, conn.PgConn(), pglogrepl.StandbyStatusUpdate{
						WALWritePosition: p.lastReceivedLSN,
						WALFlushPosition: p.lastAckedLSN,
						WALApplyPosition: p.lastAckedLSN,
					})
				}
				p.mu.Unlock()

				if err != nil {
					if !errors.Is(err, context.Canceled) {
						p.log("ERROR", "Failed to send keepalive response", "error", err)
						select {
						case p.errChan <- err:
						case <-ctx.Done():
						}
					}
					return
				}
			case 'w': // XLogData
				xld, err := pglogrepl.ParseXLogData(m.Data[1:])
				if err != nil {
					p.log("ERROR", "Failed to parse xlog data", "error", err)
					continue
				}

				p.mu.Lock()
				p.lastReceivedLSN = xld.WALStart
				p.mu.Unlock()

				logicalMsg, err := pglogrepl.Parse(xld.WALData)
				if err != nil {
					fmt.Printf("Failed to parse logical msg: %v\n", err)
					continue
				}

				switch lm := logicalMsg.(type) {
				case *pglogrepl.RelationMessage:
					p.mu.Lock()
					p.relations[lm.RelationID] = lm
					p.mu.Unlock()
				case *pglogrepl.InsertMessage:
					select {
					case p.msgChan <- p.handleInsert(xld.WALStart, lm):
					case <-ctx.Done():
						return
					}
				case *pglogrepl.UpdateMessage:
					select {
					case p.msgChan <- p.handleUpdate(xld.WALStart, lm):
					case <-ctx.Done():
						return
					}
				case *pglogrepl.DeleteMessage:
					select {
					case p.msgChan <- p.handleDelete(xld.WALStart, lm):
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}
}

func (p *PostgresSource) handleInsert(lsn pglogrepl.LSN, lm *pglogrepl.InsertMessage) hermod.Message {
	res := message.AcquireMessage()
	res.SetID(lsn.String())
	res.SetOperation(hermod.OpCreate)
	res.SetMetadata("source", "postgres")
	res.SetMetadata("lsn", lsn.String())

	p.mu.Lock()
	rel, ok := p.relations[lm.RelationID]
	p.mu.Unlock()

	if ok {
		res.SetTable(rel.RelationName)
		res.SetSchema(rel.Namespace)
		data := make(map[string]interface{})
		for i, col := range lm.Tuple.Columns {
			if i < len(rel.Columns) {
				data[rel.Columns[i].Name] = string(col.Data)
			}
		}
		jsonBytes, _ := json.Marshal(data)
		res.SetAfter(jsonBytes)
	}
	return res
}

func (p *PostgresSource) handleUpdate(lsn pglogrepl.LSN, lm *pglogrepl.UpdateMessage) hermod.Message {
	res := message.AcquireMessage()
	res.SetID(lsn.String())
	res.SetOperation(hermod.OpUpdate)
	res.SetMetadata("source", "postgres")
	res.SetMetadata("lsn", lsn.String())

	p.mu.Lock()
	rel, ok := p.relations[lm.RelationID]
	p.mu.Unlock()

	if ok {
		res.SetTable(rel.RelationName)
		res.SetSchema(rel.Namespace)
		if lm.OldTuple != nil {
			beforeData := make(map[string]interface{})
			for i, col := range lm.OldTuple.Columns {
				if i < len(rel.Columns) {
					beforeData[rel.Columns[i].Name] = string(col.Data)
				}
			}
			beforeBytes, _ := json.Marshal(beforeData)
			res.SetBefore(beforeBytes)
		}
		data := make(map[string]interface{})
		for i, col := range lm.NewTuple.Columns {
			if i < len(rel.Columns) {
				data[rel.Columns[i].Name] = string(col.Data)
			}
		}
		jsonBytes, _ := json.Marshal(data)
		res.SetAfter(jsonBytes)
	}
	return res
}

func (p *PostgresSource) handleDelete(lsn pglogrepl.LSN, lm *pglogrepl.DeleteMessage) hermod.Message {
	res := message.AcquireMessage()
	res.SetID(lsn.String())
	res.SetOperation(hermod.OpDelete)
	res.SetMetadata("source", "postgres")
	res.SetMetadata("lsn", lsn.String())

	p.mu.Lock()
	rel, ok := p.relations[lm.RelationID]
	p.mu.Unlock()

	if ok {
		res.SetTable(rel.RelationName)
		res.SetSchema(rel.Namespace)
		if lm.OldTuple != nil {
			beforeData := make(map[string]interface{})
			for i, col := range lm.OldTuple.Columns {
				if i < len(rel.Columns) {
					beforeData[rel.Columns[i].Name] = string(col.Data)
				}
			}
			beforeBytes, _ := json.Marshal(beforeData)
			res.SetBefore(beforeBytes)
		}
	}
	return res
}

func (p *PostgresSource) ensureConnNoLock(ctx context.Context) error {
	if p.conn != nil && !p.conn.IsClosed() {
		return nil
	}

	config, err := pgx.ParseConfig(p.connString)
	if err != nil {
		return fmt.Errorf("failed to parse connection string: %w", err)
	}

	if config.RuntimeParams == nil {
		config.RuntimeParams = make(map[string]string)
	}
	// Explicitly disable replication for the metadata connection
	delete(config.RuntimeParams, "replication")

	p.conn, err = pgx.ConnectConfig(ctx, config)
	if err != nil {
		return fmt.Errorf("failed to connect to postgres: %w", err)
	}
	return nil
}

func (p *PostgresSource) ensureConn(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ensureConnNoLock(ctx)
}

func (p *PostgresSource) ensureReplConnNoLock(ctx context.Context) error {
	if p.replConn != nil && !p.replConn.IsClosed() {
		return nil
	}

	connConfig, err := pgx.ParseConfig(p.connString)
	if err != nil {
		return fmt.Errorf("failed to parse connection string for replication: %w", err)
	}
	if connConfig.RuntimeParams == nil {
		connConfig.RuntimeParams = make(map[string]string)
	}
	connConfig.RuntimeParams["replication"] = "database"

	p.replConn, err = pgx.ConnectConfig(ctx, connConfig)
	if err != nil {
		return fmt.Errorf("failed to connect to postgres for replication: %w", err)
	}
	return nil
}

func (p *PostgresSource) ensureReplConn(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ensureReplConnNoLock(ctx)
}

func (p *PostgresSource) init(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.initialized {
		return nil
	}

	if err := p.ensureConnNoLock(ctx); err != nil {
		return err
	}

	if !p.useCDC {
		p.initialized = true
		return nil
	}

	if err := p.ensureReplConnNoLock(ctx); err != nil {
		return err
	}

	fmt.Printf("Initializing PostgresSource for slot %s and publication %s\n", p.slotName, p.publicationName)

	// Retry initialization a few times as some operations might take time or fail transiently
	var err error
	for attempt := 1; attempt <= 3; attempt++ {
		if err = p.ensurePublication(ctx); err == nil {
			if err = p.ensureReplicationSlot(ctx); err == nil {
				break
			}
		}
		if attempt < 3 {
			p.log("WARN", "Postgres initialization failed, retrying...", "attempt", attempt, "error", err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * time.Second):
			}
		}
	}
	if err != nil {
		return err
	}

	p.typeMap = pgtype.NewMap()

	// Start replication from LSN 0 (standard for logical slots to resume from where they left off)
	fmt.Printf("Starting replication for slot %s from LSN 0...\n", p.slotName)
	err = pglogrepl.StartReplication(ctx, p.replConn.PgConn(), p.slotName, 0, pglogrepl.StartReplicationOptions{
		PluginArgs: []string{
			"proto_version '1'",
			"publication_names '" + p.publicationName + "'",
		},
	})
	if err != nil {
		return fmt.Errorf("failed to start replication: %w", err)
	}

	fmt.Println("Replication started successfully")
	p.initialized = true
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.wg.Add(1)
	go p.streamLoop(ctx)

	return nil
}

func (p *PostgresSource) Ack(ctx context.Context, msg hermod.Message) error {
	if msg == nil {
		return nil
	}
	lsnStr := msg.Metadata()["lsn"]
	if lsnStr == "" {
		return nil
	}

	lsn, err := pglogrepl.ParseLSN(lsnStr)
	if err != nil {
		return fmt.Errorf("failed to parse LSN: %w", err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if lsn > p.lastAckedLSN {
		p.lastAckedLSN = lsn

		// Periodically update standby status
		if p.replConn != nil {
			err = pglogrepl.SendStandbyStatusUpdate(ctx, p.replConn.PgConn(), pglogrepl.StandbyStatusUpdate{
				WALWritePosition: p.lastReceivedLSN,
				WALFlushPosition: p.lastAckedLSN,
				WALApplyPosition: p.lastAckedLSN,
			})
			if err != nil {
				p.log("ERROR", "Failed to send standby status update during Ack", "error", err)
			}
		}

		// Optional: Update lag metrics here if we had access to the registry's metrics
		// For now, let's at least log if lag is high
		if p.lastReceivedLSN > p.lastAckedLSN {
			lag := uint64(p.lastReceivedLSN - p.lastAckedLSN)
			if lag > 100*1024*1024 { // 100MB
				p.log("WARN", "High replication lag detected", "lag_bytes", lag, "slot", p.slotName)
			}
		}
	}

	return nil
}

func (p *PostgresSource) Ping(ctx context.Context) error {
	// Just ensure the metadata connection is alive, don't trigger full CDC init
	if err := p.ensureConn(ctx); err != nil {
		return err
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.conn == nil {
		return errors.New("connection not initialized")
	}
	return p.conn.Ping(ctx)
}

func (p *PostgresSource) IsReady(ctx context.Context) error {
	if err := p.Ping(ctx); err != nil {
		return fmt.Errorf("postgres connection failed: %w", err)
	}

	if !p.useCDC {
		return nil
	}

	// 1) Check wal_level using a regular (non-replication) connection
	normalCfg, err := pgx.ParseConfig(p.connString)
	if err != nil {
		return fmt.Errorf("failed to parse connection string: %w", err)
	}
	if normalCfg.RuntimeParams != nil {
		delete(normalCfg.RuntimeParams, "replication")
	}
	normalConn, err := pgx.ConnectConfig(ctx, normalCfg)
	if err != nil {
		return fmt.Errorf("postgres connect (wal_level check) failed: %w", err)
	}
	var walLevel string
	if err := normalConn.QueryRow(ctx, "SHOW wal_level").Scan(&walLevel); err != nil {
		normalConn.Close(ctx)
		return fmt.Errorf("failed to check wal_level: %w", err)
	}
	// Close normal connection as early as possible
	if err := normalConn.Close(ctx); err != nil {
		return fmt.Errorf("failed closing wal_level check connection: %w", err)
	}
	if walLevel != "logical" {
		return fmt.Errorf("postgres 'wal_level' must be 'logical' for CDC (currently '%s'). Please update postgresql.conf and restart postgres", walLevel)
	}

	// 2) Attempt to open a replication connection just to validate privileges
	replCfg, err := pgx.ParseConfig(p.connString)
	if err != nil {
		return fmt.Errorf("failed to parse connection string: %w", err)
	}
	if replCfg.RuntimeParams == nil {
		replCfg.RuntimeParams = make(map[string]string)
	}
	replCfg.RuntimeParams["replication"] = "database"

	replConn, err := pgx.ConnectConfig(ctx, replCfg)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			if pgErr.Code == "28P01" {
				return fmt.Errorf("replication connection failed: invalid password. Ensure user has replication privileges and correct credentials")
			}
			if pgErr.Code == "28000" {
				return fmt.Errorf("replication connection failed: user does not have replication privileges. Run 'ALTER USER %s REPLICATION'", replCfg.User)
			}
		}
		return fmt.Errorf("replication connection failed: %w. Ensure 'wal_level' is set to 'logical' in postgresql.conf", err)
	}
	// Do not run SQL on a replication connection; simply close it if successful
	if err := replConn.Close(ctx); err != nil {
		return fmt.Errorf("failed closing replication test connection: %w", err)
	}

	return nil
}

func (p *PostgresSource) Close() error {
	p.log("INFO", "Closing PostgresSource", "slot", p.slotName, "publication", p.publicationName)
	p.mu.Lock()
	if !p.initialized {
		p.mu.Unlock()
		return nil
	}
	p.initialized = false

	if p.cancel != nil {
		p.cancel()
	}

	persistent := p.persistentSlot
	slotName := p.slotName
	publicationName := p.publicationName

	// Close connections to unblock ReceiveMessage if context cancel doesn't
	if p.replConn != nil {
		p.replConn.Close(context.Background())
	}
	if p.conn != nil {
		p.conn.Close(context.Background())
	}
	p.mu.Unlock()

	// Wait for streamLoop to finish
	p.wg.Wait()

	if !persistent {
		p.log("INFO", "Cleaning up non-persistent replication slot and publication", "slot", slotName, "publication", publicationName)
		// Need a new connection for cleanup
		conn, err := pgx.Connect(context.Background(), p.connString)
		if err == nil {
			defer conn.Close(context.Background())
			_, _ = conn.Exec(context.Background(), "SELECT pg_drop_replication_slot($1)", slotName)
			// Optional: Drop publication if it was created by us and not used elsewhere
			// _, _ = conn.Exec(context.Background(), "DROP PUBLICATION "+publicationName)
		}
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastReceivedLSN = 0
	p.lastAckedLSN = 0
	p.relations = make(map[uint32]*pglogrepl.RelationMessage)

	p.replConn = nil
	p.conn = nil

	return nil
}

func (p *PostgresSource) DiscoverDatabases(ctx context.Context) ([]string, error) {
	// Build a dedicated connection for discovery, independent of p.conn
	cfg, err := pgx.ParseConfig(p.connString)
	if err != nil {
		return nil, fmt.Errorf("parse connection string: %w", err)
	}

	// If dbname is missing or wrong, force a safe default
	if strings.TrimSpace(cfg.Database) == "" {
		cfg.Database = "postgres"
	}

	// Helper to connect with fallback on invalid_catalog_name (3D000)
	connect := func(cfg *pgx.ConnConfig) (*pgx.Conn, error) {
		conn, err := pgx.ConnectConfig(ctx, cfg)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "3D000" {
				// Try template1 if the specified DB doesn’t exist
				clone := *cfg
				clone.Database = "template1"
				return pgx.ConnectConfig(ctx, &clone)
			}
			return nil, err
		}
		return conn, nil
	}

	conn, err := connect(cfg)
	if err != nil {
		return nil, fmt.Errorf("connect for discovery: %w", err)
	}
	defer conn.Close(ctx)

	rows, err := conn.Query(ctx, "SELECT datname FROM pg_database WHERE datistemplate = false ORDER BY 1")
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

func (p *PostgresSource) DiscoverTables(ctx context.Context) ([]string, error) {
	if err := p.ensureConn(ctx); err != nil {
		return nil, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	rows, err := p.conn.Query(ctx, "SELECT schemaname || '.' || tablename FROM pg_catalog.pg_tables WHERE schemaname NOT IN ('pg_catalog', 'information_schema')")
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

func (p *PostgresSource) Sample(ctx context.Context, table string) (hermod.Message, error) {
	if err := p.ensureConn(ctx); err != nil {
		return nil, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	quoted, err := sqlutil.QuoteIdent("pgx", table)
	if err != nil {
		return nil, fmt.Errorf("invalid table name: %w", err)
	}
	rows, err := p.conn.Query(ctx, fmt.Sprintf("SELECT * FROM %s LIMIT 1", quoted))
	if err != nil {
		return nil, fmt.Errorf("failed to query sample record: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		return nil, fmt.Errorf("no records found in table %s", table)
	}

	fields := rows.FieldDescriptions()
	values, err := rows.Values()
	if err != nil {
		return nil, fmt.Errorf("failed to get values: %w", err)
	}

	record := make(map[string]interface{})
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
	msg.SetID(fmt.Sprintf("sample-%s-%d", table, time.Now().Unix()))
	msg.SetOperation(hermod.OpSnapshot)
	msg.SetTable(table)
	msg.SetAfter(afterJSON)
	msg.SetMetadata("source", "postgres")
	msg.SetMetadata("sample", "true")

	return msg, nil
}

func (p *PostgresSource) Snapshot(ctx context.Context, tables ...string) error {
	if err := p.ensureConn(ctx); err != nil {
		return err
	}

	p.mu.Lock()
	pTables := p.tables
	p.mu.Unlock()

	targetTables := tables
	if len(targetTables) == 0 {
		targetTables = pTables
	}

	if len(targetTables) == 0 {
		var err error
		targetTables, err = p.DiscoverTables(ctx)
		if err != nil {
			return err
		}
	}

	for _, table := range targetTables {
		if err := p.snapshotTable(ctx, table); err != nil {
			return err
		}
	}
	return nil
}

func (p *PostgresSource) snapshotTable(ctx context.Context, table string) error {
	quoted, err := sqlutil.QuoteIdent("pgx", table)
	if err != nil {
		return fmt.Errorf("invalid table name %q: %w", table, err)
	}

	p.mu.Lock()
	rows, err := p.conn.Query(ctx, fmt.Sprintf("SELECT * FROM %s", quoted))
	p.mu.Unlock()
	if err != nil {
		return fmt.Errorf("failed to query table %q: %w", table, err)
	}
	defer rows.Close()

	fields := rows.FieldDescriptions()
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return fmt.Errorf("failed to get values: %w", err)
		}

		record := make(map[string]interface{})
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
		msg.SetID(fmt.Sprintf("snapshot-%s-%d-%s", table, time.Now().UnixNano(), uuid.New().String()))
		msg.SetOperation(hermod.OpSnapshot)
		msg.SetTable(table)
		msg.SetAfter(afterJSON)
		msg.SetMetadata("source", "postgres")
		msg.SetMetadata("snapshot", "true")

		select {
		case p.msgChan <- msg:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return rows.Err()
}
