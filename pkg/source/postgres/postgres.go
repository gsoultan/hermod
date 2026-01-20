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

	"github.com/jackc/pglogrepl"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgproto3"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

// PostgresSource implements the hermod.Source interface for PostgreSQL CDC.
type PostgresSource struct {
	connString      string
	slotName        string
	publicationName string
	tables          []string
	useCDC          bool
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
		relations:       make(map[uint32]*pglogrepl.RelationMessage),
		msgChan:         make(chan hermod.Message, 1000),
		errChan:         make(chan error, 10),
	}
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
	var exists bool
	err := p.conn.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_publication WHERE pubname = $1)", p.publicationName).Scan(&exists)
	if err != nil {
		return fmt.Errorf("failed to check if publication exists: %w", err)
	}

	if !exists {
		tablesClause := "ALL TABLES"
		if len(p.tables) > 0 {
			tablesClause = "TABLE " + strings.Join(p.tables, ", ")
		}
		query := fmt.Sprintf("CREATE PUBLICATION %s FOR %s", p.publicationName, tablesClause)
		_, err = p.conn.Exec(ctx, query)
		if err != nil {
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
			_, err = p.conn.Exec(ctx, fmt.Sprintf("ALTER PUBLICATION %s SET FOR ALL TABLES", p.publicationName))
			if err != nil {
				return fmt.Errorf("failed to set publication to ALL TABLES: %w", err)
			}
			p.log("INFO", "Updated publication to ALL TABLES", "publication", p.publicationName)
		}
		return nil
	}

	if pubAllTables {
		// Switch from ALL TABLES to specific tables
		query := fmt.Sprintf("ALTER PUBLICATION %s SET TABLE %s", p.publicationName, strings.Join(p.tables, ", "))
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
		query := fmt.Sprintf("ALTER PUBLICATION %s SET TABLE %s", p.publicationName, strings.Join(p.tables, ", "))
		_, err = p.conn.Exec(ctx, query)
		if err != nil {
			return fmt.Errorf("failed to update publication tables: %w", err)
		}
		p.log("INFO", "Updated publication tables", "publication", p.publicationName, "tables", strings.Join(p.tables, ", "))
	}

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
				fmt.Printf("Replication stream error for slot %s: %v\n", p.slotName, err)
				select {
				case p.errChan <- err:
				case <-ctx.Done():
				}
			}
			return
		}

		switch m := msg.(type) {
		case *pgproto3.ErrorResponse:
			fmt.Printf("Postgres error response for slot %s: %s\n", p.slotName, m.Message)
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
					fmt.Printf("Failed to parse keepalive: %v\n", err)
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
						fmt.Printf("Failed to send keepalive response: %v\n", err)
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
					fmt.Printf("Failed to parse xlog data: %v\n", err)
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
	p.lastAckedLSN = lsn
	p.mu.Unlock()

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
		return err
	}

	if !p.useCDC {
		return nil
	}

	// Deep check for replication readiness
	config, err := pgx.ParseConfig(p.connString)
	if err != nil {
		return err
	}
	if config.RuntimeParams == nil {
		config.RuntimeParams = make(map[string]string)
	}
	config.RuntimeParams["replication"] = "database"

	// Try to establish a temporary replication connection to verify CDC readiness
	conn, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		return fmt.Errorf("replication readiness check failed: %w", err)
	}
	defer conn.Close(context.Background())

	return conn.Ping(ctx)
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
	if err := p.ensureConn(ctx); err != nil {
		return nil, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	rows, err := p.conn.Query(ctx, "SELECT datname FROM pg_database WHERE datistemplate = false")
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

	rows, err := p.conn.Query(ctx, fmt.Sprintf("SELECT * FROM %s LIMIT 1", table))
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
