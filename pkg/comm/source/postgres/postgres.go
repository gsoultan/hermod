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
	"github.com/user/hermod/pkg/comm/message"
	"github.com/user/hermod/pkg/infra/sqlutil"
)

// Default identifiers used when the user does not provide a replication slot
// or publication name in the CDC source form. Postgres requires both to exist
// (or be creatable) for logical replication to stream changes, so we fall back
// to these safe, valid identifiers instead of failing with empty names.
const (
	defaultSlotName        = "hermod_slot"
	defaultPublicationName = "hermod_pub"
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
	// Fall back to safe, valid identifiers when the form leaves these empty.
	// Without a valid slot/publication name Postgres cannot create the logical
	// replication slot, so no changes would ever be streamed.
	if strings.TrimSpace(slotName) == "" {
		slotName = defaultSlotName
	}
	if strings.TrimSpace(publicationName) == "" {
		publicationName = defaultPublicationName
	}
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

func (p *PostgresSource) log(level, msg string, keysAndValues ...any) {
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

	// Handle case where all tables were removed
	if len(p.tables) == 0 && !pubAllTables {
		p.log("INFO", "All tables removed from publication, cleaning up", "publication", p.publicationName)
		_ = p.dropPublicationAndSlot(ctx)
		return errors.New("all tables removed from CDC source; publication and replication slot have been cleaned up")
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

	// Check if any tables are missing OR if there are extra tables in the publication
	rows, err := p.conn.Query(ctx, "SELECT schemaname, tablename FROM pg_publication_tables WHERE pubname = $1", p.publicationName)
	if err != nil {
		return fmt.Errorf("failed to get publication tables: %w", err)
	}
	defer rows.Close()

	existingTables := make(map[string]bool)
	numInPub := 0
	for rows.Next() {
		var schema, table string
		if err := rows.Scan(&schema, &table); err != nil {
			return fmt.Errorf("failed to scan publication table: %w", err)
		}
		existingTables[table] = true
		existingTables[schema+"."+table] = true
		numInPub++
	}

	needsUpdate := false
	if numInPub != len(p.tables) {
		needsUpdate = true
	} else {
		for _, t := range p.tables {
			if !existingTables[t] {
				needsUpdate = true
				break
			}
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

func (p *PostgresSource) dropPublicationAndSlot(ctx context.Context) error {
	quotedPub, err := sqlutil.QuoteIdent("postgres", p.publicationName)
	if err == nil {
		// Try to drop publication, ignore if it doesn't exist
		_, _ = p.conn.Exec(ctx, "DROP PUBLICATION IF EXISTS "+quotedPub)
	}
	// Try to drop replication slot, ignore if it doesn't exist
	_, _ = p.conn.Exec(ctx, "SELECT pg_drop_replication_slot($1)", p.slotName)
	return nil
}

func (p *PostgresSource) createPublicationWithAllTables(ctx context.Context, quotedPub string) error {
	allTables, err := p.DiscoverTables(ctx)
	if err != nil {
		return fmt.Errorf("failed to discover tables for publication fallback: %w", err)
	}
	if len(allTables) == 0 {
		return errors.New("no tables found in database")
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
		return errors.New("no tables found in database")
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

// standbyMessageTimeout is how often we proactively send a Standby Status
// Update to Postgres while the stream is idle. Postgres terminates a walsender
// that does not hear from its standby within wal_sender_timeout (default 60s);
// without periodic heartbeats the replication connection is silently dropped
// after some idle time and CDC stops delivering changes. Sending well within
// that window keeps the connection alive across quiet periods.
const standbyMessageTimeout = 10 * time.Second

// maxStreamReconnectBackoff caps the exponential backoff used when the
// replication stream needs to be re-established (e.g. after a Postgres or
// network restart).
const maxStreamReconnectBackoff = 30 * time.Second

// slotReleaseTimeout bounds how long we wait for Postgres to mark a logical
// replication slot as inactive after terminating the backend that held it.
const slotReleaseTimeout = 10 * time.Second

// streamLoop continuously consumes the logical replication stream. It is
// designed to be resilient: when the connection drops (Postgres restart,
// worker reconnect, transient network failure) it transparently re-establishes
// the publication, slot and replication stream and resumes from the slot's
// confirmed flush LSN, so changes from tracked tables keep flowing without an
// engine restart. It only returns when the owning context is cancelled (Close).
func (p *PostgresSource) streamLoop(ctx context.Context) {
	defer p.teardownStream()
	p.log("INFO", "Starting streamLoop", "slot", p.slotName)

	backoff := time.Second
	for ctx.Err() == nil {
		conn, err := p.acquireStreamConn(ctx)
		if err != nil {
			backoff = p.waitStreamBackoff(ctx, err, backoff)
			continue
		}
		backoff = time.Second

		err = p.consumeStream(ctx, conn)
		if err == nil || ctx.Err() != nil || errors.Is(err, context.Canceled) {
			return
		}
		p.log("WARN", "Replication stream interrupted, reconnecting", "slot", p.slotName, "error", err)
		// Drop the broken connection so the next iteration recreates it.
		p.closeReplConn()
	}
}

// waitStreamBackoff logs the reconnect failure and sleeps for the current
// backoff window (honouring cancellation), returning the next backoff value.
func (p *PostgresSource) waitStreamBackoff(ctx context.Context, err error, backoff time.Duration) time.Duration {
	if ctx.Err() != nil {
		return backoff
	}
	p.log("ERROR", "Failed to (re)establish replication stream", "slot", p.slotName, "error", err)
	select {
	case <-ctx.Done():
	case <-time.After(backoff):
	}
	return min(backoff*2, maxStreamReconnectBackoff)
}

// teardownStream marks the source uninitialized and closes the replication
// connection when streamLoop exits.
func (p *PostgresSource) teardownStream() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.initialized = false
	p.closeReplConnLocked()
}

// closeReplConn closes and clears the replication connection.
func (p *PostgresSource) closeReplConn() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closeReplConnLocked()
}

// closeReplConnLocked closes and clears the replication connection. Callers
// must hold p.mu.
func (p *PostgresSource) closeReplConnLocked() {
	if p.replConn != nil {
		_ = p.replConn.Close(context.Background())
		p.replConn = nil
	}
}

// acquireStreamConn returns a live replication connection that has an active
// logical replication stream. If the current connection is healthy it is
// reused; otherwise the publication, slot and stream are re-established.
func (p *PostgresSource) acquireStreamConn(ctx context.Context) (*pgx.Conn, error) {
	p.mu.Lock()
	conn := p.replConn
	p.mu.Unlock()

	if conn != nil && !conn.IsClosed() {
		return conn, nil
	}

	if err := p.reconnectStream(ctx); err != nil {
		return nil, err
	}

	p.mu.Lock()
	conn = p.replConn
	p.mu.Unlock()
	if conn == nil {
		return nil, errors.New("replication connection unavailable after reconnect")
	}
	return conn, nil
}

// reconnectStream re-establishes everything required to stream changes: the
// metadata connection, the publication and replication slot, the replication
// connection itself, and finally the replication stream. It is safe to call
// repeatedly and resumes from the slot's confirmed flush LSN.
func (p *PostgresSource) reconnectStream(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if err := p.ensureConnNoLock(ctx); err != nil {
		return err
	}
	if err := p.ensurePublication(ctx); err != nil {
		return err
	}
	if err := p.ensureReplicationSlot(ctx); err != nil {
		return err
	}

	p.closeReplConnLocked()
	if err := p.ensureReplConnNoLock(ctx); err != nil {
		return err
	}

	p.seedLSNFromSlotLocked(ctx)
	if err := p.startReplicationWithReclaimLocked(ctx); err != nil {
		return fmt.Errorf("failed to start replication: %w", err)
	}
	p.log("INFO", "Replication stream (re)established", "slot", p.slotName, "publication", p.publicationName)
	return nil
}

// consumeStream reads from the replication connection until it errors or the
// context is cancelled. It uses a deadline-bounded receive so it can send
// periodic Standby Status Updates (heartbeats) even when no changes arrive,
// keeping the connection alive within Postgres' wal_sender_timeout window.
func (p *PostgresSource) consumeStream(ctx context.Context, conn *pgx.Conn) error {
	nextStandby := time.Now().Add(standbyMessageTimeout)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if conn.IsClosed() {
			return errors.New("replication connection closed")
		}
		if err := p.maybeSendStandby(ctx, conn, &nextStandby); err != nil {
			return err
		}

		msg, err := p.receiveMessage(ctx, conn, nextStandby)
		if err != nil {
			return err
		}
		if msg == nil {
			// Deadline reached without a message: time to send the next
			// heartbeat. The connection is still usable, so keep streaming.
			continue
		}
		if err := p.handleReplicationMessage(ctx, conn, msg); err != nil {
			return err
		}
	}
}

// maybeSendStandby sends a heartbeat Standby Status Update when the deadline
// pointed to by next has elapsed, advancing it to the next interval.
func (p *PostgresSource) maybeSendStandby(ctx context.Context, conn *pgx.Conn, next *time.Time) error {
	if time.Now().Before(*next) {
		return nil
	}
	if err := p.sendStandbyStatus(ctx, conn); err != nil {
		return fmt.Errorf("send standby status update: %w", err)
	}
	*next = time.Now().Add(standbyMessageTimeout)
	return nil
}

// receiveMessage performs a deadline-bounded receive. It returns (nil, nil)
// when the deadline elapses (signalling it is time to send a heartbeat) so the
// connection can be kept alive during idle periods.
func (p *PostgresSource) receiveMessage(ctx context.Context, conn *pgx.Conn, deadline time.Time) (pgproto3.BackendMessage, error) {
	recvCtx, cancel := context.WithDeadline(ctx, deadline)
	msg, err := conn.PgConn().ReceiveMessage(recvCtx)
	cancel()
	if err == nil {
		return msg, nil
	}
	if pgconn.Timeout(err) {
		return nil, nil
	}
	if errors.Is(err, context.Canceled) || ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return nil, err
}

// sendStandbyStatus reports the current write/flush positions to Postgres,
// confirming progress and acting as a keepalive.
func (p *PostgresSource) sendStandbyStatus(ctx context.Context, conn *pgx.Conn) error {
	p.mu.Lock()
	write := p.lastReceivedLSN
	flush := p.lastAckedLSN
	p.mu.Unlock()
	return pglogrepl.SendStandbyStatusUpdate(ctx, conn.PgConn(), pglogrepl.StandbyStatusUpdate{
		WALWritePosition: write,
		WALFlushPosition: flush,
		WALApplyPosition: flush,
	})
}

// handleReplicationMessage dispatches a single backend message received on the
// replication stream. A returned error signals the stream should be torn down
// and reconnected.
func (p *PostgresSource) handleReplicationMessage(ctx context.Context, conn *pgx.Conn, msg pgproto3.BackendMessage) error {
	switch m := msg.(type) {
	case *pgproto3.ErrorResponse:
		return fmt.Errorf("postgres error: %s", m.Message)
	case *pgproto3.CopyData:
		return p.handleCopyData(ctx, conn, m.Data)
	default:
		return nil
	}
}

// handleCopyData processes the CopyData payloads that carry keepalives and
// WAL data on the logical replication stream.
func (p *PostgresSource) handleCopyData(ctx context.Context, conn *pgx.Conn, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	switch data[0] {
	case pglogrepl.PrimaryKeepaliveMessageByteID:
		pka, err := pglogrepl.ParsePrimaryKeepaliveMessage(data[1:])
		if err != nil {
			p.log("ERROR", "Failed to parse keepalive", "error", err)
			return nil
		}
		p.mu.Lock()
		if pka.ServerWALEnd > p.lastReceivedLSN {
			p.lastReceivedLSN = pka.ServerWALEnd
		}
		p.mu.Unlock()
		if pka.ReplyRequested {
			if err := p.sendStandbyStatus(ctx, conn); err != nil {
				return fmt.Errorf("send keepalive response: %w", err)
			}
		}
		return nil
	case pglogrepl.XLogDataByteID:
		return p.handleXLogData(ctx, data[1:])
	default:
		return nil
	}
}

// handleXLogData parses a WAL data chunk and forwards any resulting change
// (insert/update/delete) to consumers, tracking relation metadata along the way.
func (p *PostgresSource) handleXLogData(ctx context.Context, data []byte) error {
	xld, err := pglogrepl.ParseXLogData(data)
	if err != nil {
		p.log("ERROR", "Failed to parse xlog data", "error", err)
		return nil
	}

	p.mu.Lock()
	if xld.WALStart > p.lastReceivedLSN {
		p.lastReceivedLSN = xld.WALStart
	}
	p.mu.Unlock()

	logicalMsg, err := pglogrepl.Parse(xld.WALData)
	if err != nil {
		p.log("ERROR", "Failed to parse logical replication message", "error", err)
		return nil
	}

	switch lm := logicalMsg.(type) {
	case *pglogrepl.RelationMessage:
		p.mu.Lock()
		p.relations[lm.RelationID] = lm
		p.mu.Unlock()
		return nil
	case *pglogrepl.InsertMessage:
		return p.dispatch(ctx, p.handleInsert(xld.WALStart, lm))
	case *pglogrepl.UpdateMessage:
		return p.dispatch(ctx, p.handleUpdate(xld.WALStart, lm))
	case *pglogrepl.DeleteMessage:
		return p.dispatch(ctx, p.handleDelete(xld.WALStart, lm))
	default:
		return nil
	}
}

// dispatch delivers a change message to consumers, respecting cancellation.
func (p *PostgresSource) dispatch(ctx context.Context, msg hermod.Message) error {
	select {
	case p.msgChan <- msg:
		return nil
	case <-ctx.Done():
		return ctx.Err()
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
		data := make(map[string]any)
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
			beforeData := make(map[string]any)
			for i, col := range lm.OldTuple.Columns {
				if i < len(rel.Columns) {
					beforeData[rel.Columns[i].Name] = string(col.Data)
				}
			}
			beforeBytes, _ := json.Marshal(beforeData)
			res.SetBefore(beforeBytes)
		}
		data := make(map[string]any)
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
			beforeData := make(map[string]any)
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

	p.log("INFO", "Initializing PostgresSource", "slot", p.slotName, "publication", p.publicationName)

	if err := p.ensurePublicationAndSlotLocked(ctx); err != nil {
		return err
	}

	// Seed the in-memory LSNs from the slot's confirmed flush position so that,
	// after a restart, standby status updates report a correct flush position
	// instead of 0 (which would otherwise be sent until the first Ack and could
	// skew replication-lag accounting).
	p.seedLSNFromSlotLocked(ctx)

	// Start replication from LSN 0 so Postgres resumes from the slot's
	// confirmed_flush_lsn (i.e. exactly where streaming left off before any
	// Postgres/worker restart) instead of losing buffered changes.
	if err := p.startReplicationWithReclaimLocked(ctx); err != nil {
		return fmt.Errorf("failed to start replication: %w", err)
	}

	p.log("INFO", "Replication started successfully", "slot", p.slotName)
	p.initialized = true
	if p.cancel != nil {
		p.cancel()
	}
	streamCtx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.wg.Go(func() {
		p.streamLoop(streamCtx)
	})

	return nil
}

// ensurePublicationAndSlotLocked makes sure the publication and replication
// slot exist, retrying a few times since these operations can fail transiently
// right after a Postgres restart. Callers must hold p.mu.
func (p *PostgresSource) ensurePublicationAndSlotLocked(ctx context.Context) error {
	var err error
	for attempt := 1; attempt <= 3; attempt++ {
		if err = p.ensurePublication(ctx); err == nil {
			if err = p.ensureReplicationSlot(ctx); err == nil {
				return nil
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
	return err
}

// startReplicationLocked begins logical replication on the current replication
// connection. Callers must hold p.mu.
func (p *PostgresSource) startReplicationLocked(ctx context.Context) error {
	if p.typeMap == nil {
		p.typeMap = pgtype.NewMap()
	}
	if p.replConn == nil {
		return errors.New("replication connection not initialized")
	}
	// Starting from LSN 0 tells Postgres to resume from the slot's
	// confirmed_flush_lsn, guaranteeing no committed changes are skipped.
	return pglogrepl.StartReplication(ctx, p.replConn.PgConn(), p.slotName, 0, pglogrepl.StartReplicationOptions{
		PluginArgs: []string{
			"proto_version '1'",
			"publication_names '" + p.publicationName + "'",
		},
	})
}

// startReplicationWithReclaimLocked starts logical replication, transparently
// reclaiming the slot if Postgres still considers it active. After an
// ungraceful Hermod/worker restart the previous walsender connection lingers
// and keeps the slot "active" until wal_sender_timeout elapses (which can be
// large or disabled), so a plain StartReplication keeps failing and CDC never
// resumes even though the worker appears online. Detecting the active-slot
// error, terminating the stale backend and retrying lets streaming resume
// immediately. Callers must hold p.mu.
func (p *PostgresSource) startReplicationWithReclaimLocked(ctx context.Context) error {
	p.reclaimSlotIfStaleLocked(ctx)
	err := p.startReplicationLocked(ctx)
	if err == nil || !isSlotActiveError(err) {
		return err
	}
	p.log("WARN", "StartReplication failed because slot is still active; reclaiming and retrying",
		"slot", p.slotName, "error", err)
	p.reclaimSlotIfStaleLocked(ctx)
	return p.startReplicationLocked(ctx)
}

// isSlotActiveError reports whether err indicates the replication slot is
// already in use by another backend (Postgres SQLSTATE 55006 / object_in_use).
func isSlotActiveError(err error) bool {
	if err == nil {
		return false
	}
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == "55006" {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "is active for pid") || strings.Contains(msg, "already active")
}

// reclaimSlotIfStaleLocked terminates the backend that currently holds the
// replication slot, if any, so this source can take over streaming. This is
// the key to resuming CDC after an ungraceful restart, where the slot remains
// bound to a dead connection. The metadata connection (p.conn) is used because
// the replication connection cannot run regular SQL. Failures are non-fatal:
// the caller retries and falls back to normal backoff. Callers must hold p.mu.
func (p *PostgresSource) reclaimSlotIfStaleLocked(ctx context.Context) {
	if p.conn == nil {
		return
	}
	var activePID *int32
	err := p.conn.QueryRow(ctx,
		"SELECT active_pid FROM pg_replication_slots WHERE slot_name = $1 AND active",
		p.slotName,
	).Scan(&activePID)
	if err != nil || activePID == nil {
		return
	}
	p.log("WARN", "Replication slot held by a stale backend; terminating it to take over",
		"slot", p.slotName, "active_pid", *activePID)
	if _, err := p.conn.Exec(ctx, "SELECT pg_terminate_backend($1)", *activePID); err != nil {
		p.log("WARN", "Failed to terminate stale slot holder",
			"slot", p.slotName, "active_pid", *activePID, "error", err)
		return
	}
	p.waitSlotReleasedLocked(ctx)
}

// waitSlotReleasedLocked polls until the slot is no longer active or the
// slotReleaseTimeout elapses, since Postgres releases the slot a moment after
// the holding backend is terminated. Callers must hold p.mu.
func (p *PostgresSource) waitSlotReleasedLocked(ctx context.Context) {
	deadline := time.Now().Add(slotReleaseTimeout)
	for time.Now().Before(deadline) {
		var active bool
		err := p.conn.QueryRow(ctx,
			"SELECT COALESCE((SELECT active FROM pg_replication_slots WHERE slot_name = $1), false)",
			p.slotName,
		).Scan(&active)
		if err != nil || !active {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// seedLSNFromSlotLocked initializes lastReceivedLSN/lastAckedLSN from the
// slot's confirmed_flush_lsn so that, after any restart, progress reporting
// starts from the real persisted position rather than 0. Callers must hold
// p.mu. Failures are non-fatal: streaming can still proceed from the slot.
func (p *PostgresSource) seedLSNFromSlotLocked(ctx context.Context) {
	if p.conn == nil {
		return
	}
	var lsnText *string
	err := p.conn.QueryRow(ctx,
		"SELECT confirmed_flush_lsn::text FROM pg_replication_slots WHERE slot_name = $1",
		p.slotName,
	).Scan(&lsnText)
	if err != nil || lsnText == nil || *lsnText == "" {
		return
	}
	lsn, err := pglogrepl.ParseLSN(*lsnText)
	if err != nil {
		return
	}
	if lsn > p.lastAckedLSN {
		p.lastAckedLSN = lsn
	}
	if lsn > p.lastReceivedLSN {
		p.lastReceivedLSN = lsn
	}
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
		_ = normalConn.Close(ctx)
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
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
			if pgErr.Code == "28P01" {
				return errors.New("replication connection failed: invalid password. Ensure user has replication privileges and correct credentials")
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

	// Even when the source was never fully initialized for CDC streaming, the
	// metadata connection (p.conn) may have been opened by lightweight
	// operations such as Ping (test connection), DiscoverTables/Columns or
	// replication-slot/publication discovery. Those paths call ensureConn
	// without setting p.initialized, so an early return here would leak the
	// underlying pgx connection on every test/fetch request and eventually
	// exhaust the database/file-descriptor limits, taking the worker offline.
	wasInitialized := p.initialized
	p.initialized = false

	if p.cancel != nil {
		p.cancel()
	}

	persistent := p.persistentSlot
	slotName := p.slotName
	publicationName := p.publicationName

	// Close connections to unblock ReceiveMessage if context cancel doesn't
	p.closeReplConnLocked()
	if p.conn != nil {
		_ = p.conn.Close(context.Background())
	}
	p.mu.Unlock()

	// Wait for streamLoop to finish
	p.wg.Wait()

	// Only attempt slot/publication cleanup when CDC streaming was actually
	// initialized; otherwise no replication slot was created by this source.
	if wasInitialized && !persistent {
		p.log("INFO", "Cleaning up non-persistent replication slot and publication", "slot", slotName, "publication", publicationName)
		// Need a new connection for cleanup
		conn, err := pgx.Connect(context.Background(), p.connString)
		if err == nil {
			defer func() { _ = conn.Close(context.Background()) }()
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
			if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == "3D000" {
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
	defer func() { _ = conn.Close(ctx) }()

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

// DiscoverReplicationSlots returns all logical replication slots present in the
// connected PostgreSQL instance. The UI uses this so users can reuse an existing
// slot instead of always creating a new one.
func (p *PostgresSource) DiscoverReplicationSlots(ctx context.Context) ([]hermod.ReplicationSlotInfo, error) {
	if err := p.ensureConn(ctx); err != nil {
		return nil, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	rows, err := p.conn.Query(ctx, "SELECT slot_name, COALESCE(plugin, ''), COALESCE(slot_type, ''), COALESCE(database, ''), active FROM pg_replication_slots ORDER BY slot_name")
	if err != nil {
		return nil, fmt.Errorf("failed to query replication slots: %w", err)
	}
	defer rows.Close()

	slots := []hermod.ReplicationSlotInfo{}
	for rows.Next() {
		var s hermod.ReplicationSlotInfo
		if err := rows.Scan(&s.Name, &s.Plugin, &s.SlotType, &s.Database, &s.Active); err != nil {
			return nil, err
		}
		slots = append(slots, s)
	}
	return slots, rows.Err()
}

// DiscoverPublications returns all publications and the tables each one covers
// so the user can pick an existing publication that already includes their table
// or decide to create a new one.
func (p *PostgresSource) DiscoverPublications(ctx context.Context) ([]hermod.PublicationInfo, error) {
	if err := p.ensureConn(ctx); err != nil {
		return nil, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	rows, err := p.conn.Query(ctx, "SELECT pubname, puballtables FROM pg_publication ORDER BY pubname")
	if err != nil {
		return nil, fmt.Errorf("failed to query publications: %w", err)
	}
	defer rows.Close()

	pubs := []hermod.PublicationInfo{}
	for rows.Next() {
		var pub hermod.PublicationInfo
		if err := rows.Scan(&pub.Name, &pub.AllTables); err != nil {
			return nil, err
		}
		pub.Tables = []string{}
		pubs = append(pubs, pub)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Populate the covered tables for publications that target specific tables.
	for i := range pubs {
		if pubs[i].AllTables {
			continue
		}
		tableRows, err := p.conn.Query(ctx, "SELECT schemaname || '.' || tablename FROM pg_publication_tables WHERE pubname = $1 ORDER BY 1", pubs[i].Name)
		if err != nil {
			return nil, fmt.Errorf("failed to query publication tables: %w", err)
		}
		for tableRows.Next() {
			var t string
			if err := tableRows.Scan(&t); err != nil {
				tableRows.Close()
				return nil, err
			}
			pubs[i].Tables = append(pubs[i].Tables, t)
		}
		err = tableRows.Err()
		tableRows.Close()
		if err != nil {
			return nil, err
		}
	}
	return pubs, nil
}

func (p *PostgresSource) DiscoverColumns(ctx context.Context, table string) ([]hermod.ColumnInfo, error) {
	if err := p.ensureConn(ctx); err != nil {
		return nil, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	query := `
		SELECT column_name, data_type, COALESCE(is_nullable = 'YES', false), 
		       EXISTS (
		           SELECT 1 FROM information_schema.key_column_usage kcu
		           JOIN information_schema.table_constraints tc ON kcu.constraint_name = tc.constraint_name
		           WHERE (kcu.table_name = $1 OR kcu.table_schema || '.' || kcu.table_name = $1) 
		           AND tc.constraint_type = 'PRIMARY KEY' AND kcu.column_name = columns.column_name
		       ) as is_pk,
		       COALESCE(is_identity = 'YES' OR column_default LIKE 'nextval%', false),
		       column_default
		FROM information_schema.columns
		WHERE table_name = $1 OR table_schema || '.' || table_name = $1
		ORDER BY ordinal_position`

	rows, err := p.conn.Query(ctx, query, table)
	if err != nil {
		return nil, fmt.Errorf("failed to query columns: %w", err)
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
	rows, err := p.conn.Query(ctx, "SELECT * FROM "+quoted)
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

func (p *PostgresSource) ExecuteSQL(ctx context.Context, query string) ([]map[string]any, error) {
	if err := p.ensureConn(ctx); err != nil {
		return nil, err
	}

	p.mu.Lock()
	rows, err := p.conn.Query(ctx, query)
	p.mu.Unlock()
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	fields := rows.FieldDescriptions()
	var results []map[string]any
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return nil, err
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
		results = append(results, record)
	}
	return results, nil
}
