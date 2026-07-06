package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
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
	sourcebuf "github.com/user/hermod/pkg/comm/source"
	"github.com/user/hermod/pkg/infra/pgxutil"
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

// replicationAppNamePrefix tags the logical replication connection so a
// restarted source can recognise (and reclaim) its own orphaned walsender,
// while never terminating a foreign consumer of the same slot.
const replicationAppNamePrefix = "hermod-cdc-"

// maxAppNameLen bounds the application_name to Postgres' NAMEDATALEN-1 limit so
// the value we store matches the value we later read back from
// pg_stat_activity (Postgres silently truncates longer names, which would
// otherwise break the own-orphan equality check).
const maxAppNameLen = 63

// buildReplicationAppName derives a stable, instance-unique application_name
// from the host and slot. It is stable across restarts of the same worker (so
// an orphaned walsender from a previous run is recognised as our own) yet
// distinct from other hosts/slots (so we never reclaim a foreign consumer).
func buildReplicationAppName(host, slotName string) string {
	if strings.TrimSpace(host) == "" {
		host = "unknown"
	}
	name := replicationAppNamePrefix + host + "-" + slotName
	if len(name) > maxAppNameLen {
		name = name[:maxAppNameLen]
	}
	return name
}

// hostnameOrUnknown returns the OS hostname, falling back to a constant when it
// cannot be determined so the derived application_name is always non-empty.
func hostnameOrUnknown() string {
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		return "unknown"
	}
	return host
}

// PostgresSource implements the hermod.Source interface for PostgreSQL CDC.
type PostgresSource struct {
	connString      string
	slotName        string
	publicationName string
	tables          []string
	useCDC          bool
	pooled          bool      // Whether connString targets a transaction/statement pooler (PgBouncer)
	persistentSlot  bool      // Whether to keep the slot on Close
	appName         string    // Stable, instance-unique application_name for the replication connection
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
	initMu          sync.Mutex
	wg              sync.WaitGroup
	// logMu guards logger only. It is intentionally separate from mu: log() is
	// called from many code paths that already hold mu (init, Close and every
	// *Locked helper). Since mu is a non-reentrant sync.Mutex, having log()
	// acquire mu would self-deadlock and silently freeze the streaming goroutine
	// (the source would appear "online" but never deliver changes). A dedicated
	// lock lets logging run safely regardless of whether mu is held.
	logMu  sync.RWMutex
	logger hermod.Logger
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
		pooled:          pgxutil.IsPooledConnString(connString),
		persistentSlot:  true, // Default to persistent for reliability
		appName:         buildReplicationAppName(hostnameOrUnknown(), slotName),
		relations:       make(map[uint32]*pglogrepl.RelationMessage),
		msgChan:         make(chan hermod.Message, sourcebuf.DefaultSourceBuffer),
		errChan:         make(chan error, 10),
	}
}

func (p *PostgresSource) SetPersistentSlot(persistent bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.persistentSlot = persistent
}

func (p *PostgresSource) SetLogger(logger hermod.Logger) {
	p.logMu.Lock()
	defer p.logMu.Unlock()
	p.logger = logger
}

func (p *PostgresSource) log(level, msg string, keysAndValues ...any) {
	// Use logMu (not mu): log() is frequently called by callers that already
	// hold mu, so locking mu here would deadlock (see the logMu field comment).
	p.logMu.RLock()
	logger := p.logger
	p.logMu.RUnlock()

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

	exists, err := p.publicationExists(ctx)
	if err != nil {
		return err
	}
	if !exists {
		return p.createPublication(ctx, quotedPub)
	}
	return p.reconcileExistingPublication(ctx, quotedPub)
}

// publicationExists reports whether the configured publication is already
// present in the database.
func (p *PostgresSource) publicationExists(ctx context.Context) (bool, error) {
	var exists bool
	err := p.conn.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_publication WHERE pubname = $1)", p.publicationName).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("failed to check if publication exists: %w", err)
	}
	return exists, nil
}

// createPublication creates the publication, covering either the configured
// table list or ALL TABLES, with a non-superuser fallback for the latter.
func (p *PostgresSource) createPublication(ctx context.Context, quotedPub string) error {
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
	if _, err := p.conn.Exec(ctx, query); err != nil {
		// Fallback for non-superuser if ALL TABLES failed.
		if len(p.tables) == 0 && (strings.Contains(err.Error(), "superuser") || strings.Contains(err.Error(), "permission")) {
			p.log("WARN", "Failed to create publication FOR ALL TABLES (need superuser), falling back to listing all tables", "error", err)
			return p.createPublicationWithAllTables(ctx, quotedPub)
		}
		return fmt.Errorf("failed to create publication: %w", err)
	}
	p.log("INFO", "Created publication", "publication", p.publicationName)
	return nil
}

// reconcileExistingPublication aligns an already-existing publication with the
// configured table list, without ever dropping an externally-managed one.
func (p *PostgresSource) reconcileExistingPublication(ctx context.Context, quotedPub string) error {
	var pubAllTables bool
	err := p.conn.QueryRow(ctx, "SELECT puballtables FROM pg_publication WHERE pubname = $1", p.publicationName).Scan(&pubAllTables)
	if err != nil {
		return fmt.Errorf("failed to check publication details: %w", err)
	}

	// An empty tables config means "Hermod is not managing a specific table
	// list". When the publication already exists we must NOT destroy it: it may
	// be externally managed (e.g. created as CREATE PUBLICATION ... FOR TABLE
	// ...). Dropping it here would silently stop all CDC and is the most common
	// cause of a source that "receives no data". Instead we adopt the existing
	// publication exactly as it is and stream whatever it already covers.
	if len(p.tables) == 0 {
		if !pubAllTables {
			p.log("INFO",
				"Adopting existing publication as-is; no table list configured so Hermod will not modify it",
				"publication", p.publicationName)
		}
		return nil
	}

	if pubAllTables {
		return p.setPublicationTables(ctx, quotedPub, "Updated publication from ALL TABLES to specific tables")
	}

	needsUpdate, err := p.publicationNeedsTableUpdate(ctx)
	if err != nil {
		return err
	}
	if needsUpdate {
		return p.setPublicationTables(ctx, quotedPub, "Updated publication tables")
	}
	return nil
}

// publicationNeedsTableUpdate reports whether the publication's current table
// set differs from the configured table list.
func (p *PostgresSource) publicationNeedsTableUpdate(ctx context.Context) (bool, error) {
	rows, err := p.conn.Query(ctx, "SELECT schemaname, tablename FROM pg_publication_tables WHERE pubname = $1", p.publicationName)
	if err != nil {
		return false, fmt.Errorf("failed to get publication tables: %w", err)
	}
	defer rows.Close()

	existingTables := make(map[string]bool)
	numInPub := 0
	for rows.Next() {
		var schema, table string
		if err := rows.Scan(&schema, &table); err != nil {
			return false, fmt.Errorf("failed to scan publication table: %w", err)
		}
		existingTables[table] = true
		existingTables[schema+"."+table] = true
		numInPub++
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("failed to read publication tables: %w", err)
	}

	if numInPub != len(p.tables) {
		return true, nil
	}
	for _, t := range p.tables {
		if !existingTables[t] {
			return true, nil
		}
	}
	return false, nil
}

// setPublicationTables sets the publication to cover exactly the configured
// table list, logging the provided message on success.
func (p *PostgresSource) setPublicationTables(ctx context.Context, quotedPub, logMsg string) error {
	quotedTables := make([]string, len(p.tables))
	for i, t := range p.tables {
		quotedTables[i], _ = sqlutil.QuoteIdent("postgres", t)
	}
	query := fmt.Sprintf("ALTER PUBLICATION %s SET TABLE %s", quotedPub, strings.Join(quotedTables, ", "))
	if _, err := p.conn.Exec(ctx, query); err != nil {
		return fmt.Errorf("failed to update publication tables: %w", err)
	}
	p.log("INFO", logMsg, "publication", p.publicationName, "tables", strings.Join(p.tables, ", "))
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

// idleHeartbeatLogInterval controls how often an INFO "awaiting changes" line
// is emitted to the live log while the replication stream is connected but no
// changes are arriving. This keeps the workflow live log informative during
// quiet periods without flooding it.
const idleHeartbeatLogInterval = 30 * time.Second

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
	if err := p.ensureConn(ctx); err != nil {
		return err
	}
	if err := p.ensurePublication(ctx); err != nil {
		return err
	}
	if err := p.ensureReplicationSlot(ctx); err != nil {
		return err
	}

	if err := p.ensureReplConn(ctx); err != nil {
		return err
	}

	p.mu.Lock()
	p.seedLSNFromSlotLocked(ctx)
	p.mu.Unlock()

	if err := p.startReplicationWithReclaim(ctx); err != nil {
		return fmt.Errorf("failed to start replication: %w", err)
	}

	p.mu.Lock()
	slotName := p.slotName
	pubName := p.publicationName
	p.mu.Unlock()

	p.log("INFO", "Replication stream (re)established", "slot", slotName, "publication", pubName)
	return nil
}

// consumeStream reads from the replication connection until it errors or the
// context is cancelled. It uses a deadline-bounded receive so it can send
// periodic Standby Status Updates (heartbeats) even when no changes arrive,
// keeping the connection alive within Postgres' wal_sender_timeout window.
func (p *PostgresSource) consumeStream(ctx context.Context, conn *pgx.Conn) error {
	nextStandby := time.Now().Add(standbyMessageTimeout)
	nextHeartbeatLog := time.Now().Add(idleHeartbeatLogInterval)
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
			// Periodically surface an INFO heartbeat to the live log so an
			// idle-but-healthy source is clearly distinguishable from a dead
			// one (otherwise the live log stays empty and looks broken).
			if time.Now().After(nextHeartbeatLog) {
				p.log("INFO", "CDC connected, awaiting changes",
					"slot", p.slotName,
					"publication", p.publicationName,
					"last_received_lsn", p.snapshotLastReceivedLSN().String())
				nextHeartbeatLog = time.Now().Add(idleHeartbeatLogInterval)
			}
			continue
		}
		if err := p.handleReplicationMessage(ctx, conn, msg); err != nil {
			return err
		}
	}
}

// snapshotLastReceivedLSN returns the last received LSN under lock, for safe
// inclusion in diagnostic log lines from the streaming goroutine.
func (p *PostgresSource) snapshotLastReceivedLSN() pglogrepl.LSN {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastReceivedLSN
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
	if errors.Is(err, context.DeadlineExceeded) || pgconn.Timeout(err) {
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

	logicalMsg, err := pglogrepl.Parse(xld.WALData)
	if err != nil {
		p.log("ERROR", "Failed to parse logical replication message", "error", err)
		return nil
	}

	currentLSN := xld.WALStart + pglogrepl.LSN(len(xld.WALData))

	p.mu.Lock()
	if currentLSN > p.lastReceivedLSN {
		p.lastReceivedLSN = currentLSN
	}
	p.mu.Unlock()

	switch lm := logicalMsg.(type) {
	case *pglogrepl.RelationMessage:
		p.mu.Lock()
		p.relations[lm.RelationID] = lm
		p.mu.Unlock()
		return nil
	case *pglogrepl.InsertMessage:
		return p.dispatch(ctx, p.handleInsert(currentLSN, lm))
	case *pglogrepl.UpdateMessage:
		return p.dispatch(ctx, p.handleUpdate(currentLSN, lm))
	case *pglogrepl.DeleteMessage:
		return p.dispatch(ctx, p.handleDelete(currentLSN, lm))
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

	if ok && lm.Tuple != nil {
		res.SetTable(rel.RelationName)
		res.SetSchema(rel.Namespace)
		data := make(map[string]any)
		for i, col := range lm.Tuple.Columns {
			if i < len(rel.Columns) {
				name := rel.Columns[i].Name
				switch col.DataType {
				case 'n': // Null
					data[name] = nil
				case 't': // Text
					data[name] = string(col.Data)
				case 'b': // Binary
					data[name] = col.Data
				}
			}
		}
		jsonBytes, err := json.Marshal(data)
		if err == nil {
			res.SetAfter(jsonBytes)
		}
	} else if !ok {
		p.log("WARN", "Received Insert for unknown relation", "relation_id", lm.RelationID)
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
					name := rel.Columns[i].Name
					switch col.DataType {
					case 'n': // Null
						beforeData[name] = nil
					case 't': // Text
						beforeData[name] = string(col.Data)
					case 'b': // Binary
						beforeData[name] = col.Data
					}
				}
			}
			beforeBytes, err := json.Marshal(beforeData)
			if err == nil {
				res.SetBefore(beforeBytes)
			}
		}
		if lm.NewTuple != nil {
			data := make(map[string]any)
			for i, col := range lm.NewTuple.Columns {
				if i < len(rel.Columns) {
					name := rel.Columns[i].Name
					switch col.DataType {
					case 'n': // Null
						data[name] = nil
					case 't': // Text
						data[name] = string(col.Data)
					case 'b': // Binary
						data[name] = col.Data
					}
				}
			}
			jsonBytes, err := json.Marshal(data)
			if err == nil {
				res.SetAfter(jsonBytes)
			}
		}
	} else {
		p.log("WARN", "Received Update for unknown relation", "relation_id", lm.RelationID)
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
					name := rel.Columns[i].Name
					switch col.DataType {
					case 'n': // Null
						beforeData[name] = nil
					case 't': // Text
						beforeData[name] = string(col.Data)
					case 'b': // Binary
						beforeData[name] = col.Data
					}
				}
			}
			beforeBytes, err := json.Marshal(beforeData)
			if err == nil {
				res.SetBefore(beforeBytes)
			}
		}
	} else {
		p.log("WARN", "Received Delete for unknown relation", "relation_id", lm.RelationID)
	}
	return res
}

// openMetadataConn dials a fresh, non-replication metadata connection using the
// pooler-safe pgx configuration. Callers own the returned connection and must
// close it.
func (p *PostgresSource) openMetadataConn(ctx context.Context) (*pgx.Conn, error) {
	config, pooled, err := pgxutil.ParseConfig(p.connString)
	if err != nil {
		return nil, fmt.Errorf("failed to parse connection string: %w", err)
	}

	// Update pooled status safely
	p.mu.Lock()
	p.pooled = pooled
	p.mu.Unlock()

	if config.RuntimeParams == nil {
		config.RuntimeParams = make(map[string]string)
	}
	// Explicitly disable replication for the metadata connection.
	delete(config.RuntimeParams, "replication")
	return pgx.ConnectConfig(ctx, config)
}

func (p *PostgresSource) ensureConn(ctx context.Context) error {
	p.mu.Lock()
	if p.conn != nil && !p.conn.IsClosed() {
		p.mu.Unlock()
		return nil
	}
	p.mu.Unlock()

	// Perform I/O without holding the lock to prevent deadlocking Close() during timeouts
	conn, err := p.openMetadataConn(ctx)
	if err != nil {
		return fmt.Errorf("failed to connect to postgres: %w", err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.conn != nil && !p.conn.IsClosed() {
		_ = conn.Close(ctx)
		return nil
	}
	p.conn = conn
	return nil
}

func (p *PostgresSource) ensureReplConn(ctx context.Context) error {
	p.mu.Lock()
	if p.replConn != nil && !p.replConn.IsClosed() {
		p.mu.Unlock()
		return nil
	}
	p.mu.Unlock()

	// Logical replication establishes a long-lived session-scoped connection and
	// uses the replication protocol, neither of which is supported by a
	// transaction/statement pooling proxy such as PgBouncer. Fail fast with an
	// actionable message instead of hanging until the request deadline.
	p.mu.Lock()
	isPooled := p.pooled
	p.mu.Unlock()

	if isPooled {
		return errors.New("CDC requires a direct Postgres connection; PgBouncer transaction/statement mode does not support logical replication. Provide a direct (session-mode) connection string for CDC sources")
	}

	// Strip the custom pooler markers (pgbouncer/pool_mode) before handing the
	// string to pgx; otherwise pgx forwards them as Postgres startup parameters
	// and the connection handshake fails.
	connConfig, _, err := pgxutil.ParseConfig(p.connString)
	if err != nil {
		return fmt.Errorf("failed to parse connection string for replication: %w", err)
	}
	if connConfig.RuntimeParams == nil {
		connConfig.RuntimeParams = make(map[string]string)
	}
	connConfig.RuntimeParams["replication"] = "database"

	p.mu.Lock()
	appName := p.appName
	p.mu.Unlock()

	if strings.TrimSpace(appName) != "" {
		connConfig.RuntimeParams["application_name"] = appName
	}

	replConn, err := pgx.ConnectConfig(ctx, connConfig)
	if err != nil {
		return fmt.Errorf("failed to connect to postgres for replication: %w", err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.closeReplConnLocked()
	p.replConn = replConn
	return nil
}

func (p *PostgresSource) ensureConnNoLock(ctx context.Context) error {
	if p.conn != nil && !p.conn.IsClosed() {
		return nil
	}
	return errors.New("connection not established (call ensureConn first)")
}

func (p *PostgresSource) ensureReplConnNoLock(ctx context.Context) error {
	if p.replConn != nil && !p.replConn.IsClosed() {
		return nil
	}
	if p.pooled {
		return errors.New("CDC requires a direct Postgres connection; PgBouncer transaction/statement mode does not support logical replication. Provide a direct (session-mode) connection string for CDC sources")
	}
	return errors.New("replication connection not established (call ensureReplConn first)")
}

func (p *PostgresSource) init(ctx context.Context) error {
	p.mu.Lock()
	if p.initialized {
		p.mu.Unlock()
		return nil
	}
	p.mu.Unlock()

	p.initMu.Lock()
	defer p.initMu.Unlock()

	// Re-check initialized under lock to prevent concurrent initialization
	p.mu.Lock()
	if p.initialized {
		p.mu.Unlock()
		return nil
	}
	p.mu.Unlock()

	// Perform connection establishment without holding p.mu to avoid blocking
	// Ping/IsReady/Close calls during potentially slow network/DB I/O.
	if err := p.ensureConn(ctx); err != nil {
		return err
	}

	// Perform the actual initialization logic.
	if err := p.initialize(ctx); err != nil {
		// Surface startup/permission failures (wrong connection mode, missing
		// wal_level, publication/slot problems, privilege errors) to the live
		// log so they are visible in the workflow editor instead of only in the
		// process output. These errors happen before any message flows, so the
		// live log would otherwise stay empty and the source would look idle.
		if ctx.Err() == nil {
			p.log("ERROR", "PostgresSource initialization failed",
				"slot", p.slotName, "publication", p.publicationName, "error", err)
		}
		return err
	}
	return nil
}

// initialize performs the actual initialization. It manages its own locking
// of p.mu to avoid holding it during long I/O or retries.
func (p *PostgresSource) initialize(ctx context.Context) error {
	p.mu.Lock()
	if p.initialized {
		p.mu.Unlock()
		return nil
	}

	if err := p.ensureConnNoLock(ctx); err != nil {
		p.mu.Unlock()
		return err
	}

	if !p.useCDC {
		p.initialized = true
		p.mu.Unlock()
		return nil
	}

	p.mu.Unlock()

	p.log("INFO", "Initializing PostgresSource", "slot", p.slotName, "publication", p.publicationName)

	// ensurePublicationAndSlot handles its own retry loop and releases/re-acquires the lock
	if err := p.ensurePublicationAndSlot(ctx); err != nil {
		return err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Double check context hasn't been cancelled during I/O
	if err := ctx.Err(); err != nil {
		return err
	}

	// Seed the in-memory LSNs from the slot's confirmed flush position so that,
	// after a restart, standby status updates report a correct flush position
	// instead of 0 (which would otherwise be sent until the first Ack and could
	// skew replication-lag accounting).
	p.seedLSNFromSlotLocked(ctx)

	p.initialized = true
	if p.cancel != nil {
		p.cancel()
	}
	streamCtx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.wg.Go(func() {
		p.streamLoop(streamCtx)
	})

	p.log("INFO", "PostgresSource initialized", "slot", p.slotName)
	return nil
}

// ensurePublicationAndSlot makes sure the publication and replication
// slot exist, retrying a few times since these operations can fail transiently
// right after a Postgres restart. It does not hold p.mu during I/O or sleep.
func (p *PostgresSource) ensurePublicationAndSlot(ctx context.Context) error {
	var err error
	for attempt := 1; attempt <= 3; attempt++ {
		// Use p.conn for metadata operations. ensurePublication/ReplicationSlot
		// don't hold the lock but use p.conn. We assume it's stable because of initMu.
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
func (p *PostgresSource) startReplicationWithReclaim(ctx context.Context) error {
	p.reclaimSlotIfStale(ctx)
	p.mu.Lock()
	err := p.startReplicationLocked(ctx)
	p.mu.Unlock()
	if err == nil || !isSlotActiveError(err) {
		return err
	}
	p.log("WARN", "StartReplication failed because slot is still active; reclaiming and retrying",
		"slot", p.slotName, "error", err)
	p.reclaimSlotIfStale(ctx)
	p.mu.Lock()
	defer p.mu.Unlock()
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
// replication slot when that backend is safe to reclaim, namely:
//
//   - it is genuinely dead (its PID is no longer present in pg_stat_activity,
//     e.g. the previous walsender died in an ungraceful Hermod/worker crash but
//     the slot is still marked active during the TCP keepalive grace window); or
//   - it is this source's OWN orphaned walsender, left behind by a previous
//     ungraceful run of the same worker, recognised by our instance-unique
//     application_name (see isOwnOrphanLocked). Reclaiming our own orphan lets a
//     restarted worker re-attach to a persistent slot immediately instead of
//     looping on the "is active for PID …" error until wal_sender_timeout
//     elapses.
//
// It deliberately does NOT terminate a foreign live holder. This is critical for
// stability: when more than one engine instance contends for the same slot
// (overlapping restarts, multiple worker processes — the production logs showed
// several hermod PIDs all "Closing PostgresSource" on the same slot), blindly
// terminating whichever backend holds the slot makes the instances kill each
// other's healthy walsender in an endless ping-pong, so no stream ever survives
// and CDC silently delivers nothing while the worker looks online. By reclaiming
// only dead holders and our own orphans, two live instances never fight: the
// loser simply backs off (single-consumer-per-slot is the Hermod convention and
// is enforced by the worker lease layer). The metadata connection (p.conn) is
// used because the replication connection cannot run regular SQL. Failures are
// non-fatal: the caller retries and falls back to normal backoff. Callers must
// hold p.mu.
// reclaimSlotIfStale terminates the backend that currently holds the
// replication slot when that backend is safe to reclaim. It does not hold
// p.mu during I/O or sleep.
func (p *PostgresSource) reclaimSlotIfStale(ctx context.Context) {
	p.mu.Lock()
	conn := p.conn
	slotName := p.slotName
	if conn == nil {
		p.mu.Unlock()
		return
	}
	p.mu.Unlock()

	var activePID *int32
	var holderAlive bool
	var holderAppName string
	err := conn.QueryRow(ctx,
		`SELECT s.active_pid,
		        EXISTS (SELECT 1 FROM pg_stat_activity a WHERE a.pid = s.active_pid),
		        COALESCE((SELECT a.application_name FROM pg_stat_activity a
		                    WHERE a.pid = s.active_pid), '')
		   FROM pg_replication_slots s
		  WHERE s.slot_name = $1 AND s.active`,
		slotName,
	).Scan(&activePID, &holderAlive, &holderAppName)
	if err != nil || activePID == nil {
		return
	}

	p.mu.Lock()
	isOwn := p.isOwnOrphanLocked(*activePID, holderAppName)
	p.mu.Unlock()

	if holderAlive && !isOwn {
		// A foreign live backend is streaming from this slot (a different host,
		// slot consumer or our own current connection). Terminating it would
		// start a mutual-kill loop, so leave it alone and let the caller back
		// off (single-consumer-per-slot is the Hermod convention).
		p.log("INFO", "Replication slot held by a live backend; backing off instead of terminating",
			"slot", slotName, "active_pid", *activePID, "holder_application_name", holderAppName)
		return
	}

	if holderAlive {
		p.log("WARN", "Replication slot held by our own orphaned walsender; terminating it to take over",
			"slot", slotName, "active_pid", *activePID, "application_name", holderAppName)
	} else {
		p.log("WARN", "Replication slot held by a dead backend; terminating it to take over",
			"slot", slotName, "active_pid", *activePID)
	}

	if _, err := conn.Exec(ctx, "SELECT pg_terminate_backend($1)", *activePID); err != nil {
		p.log("WARN", "Failed to terminate stale slot holder",
			"slot", slotName, "active_pid", *activePID, "error", err)
		return
	}
	p.waitSlotReleased(ctx)
}

// isOwnOrphanLocked reports whether a live slot holder is this source's own
// previous walsender, left behind by an ungraceful restart, rather than a
// foreign live consumer. It is treated as our orphan only when the holder
// advertises our instance-unique application_name AND its PID differs from our
// current replication connection's backend PID, so we never terminate our own
// active stream nor a different host/consumer. Callers must hold p.mu.
func (p *PostgresSource) isOwnOrphanLocked(activePID int32, holderAppName string) bool {
	if strings.TrimSpace(p.appName) == "" || holderAppName != p.appName {
		return false
	}
	if p.replConn != nil && !p.replConn.IsClosed() {
		if pgConn := p.replConn.PgConn(); pgConn != nil && int64(pgConn.PID()) == int64(activePID) {
			return false
		}
	}
	return true
}

// waitSlotReleased polls until the slot is no longer active or the
// slotReleaseTimeout elapses. It does not hold p.mu during I/O or sleep.
func (p *PostgresSource) waitSlotReleased(ctx context.Context) {
	p.mu.Lock()
	conn := p.conn
	slotName := p.slotName
	p.mu.Unlock()

	if conn == nil {
		return
	}

	deadline := time.Now().Add(slotReleaseTimeout)
	for time.Now().Before(deadline) {
		var active bool
		err := conn.QueryRow(ctx,
			"SELECT COALESCE((SELECT active FROM pg_replication_slots WHERE slot_name = $1), false)",
			slotName,
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
	// Create a new connection for testing and close it immediately.
	conn, err := p.openMetadataConn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(ctx) }()

	return conn.Ping(ctx)
}

func (p *PostgresSource) IsReady(ctx context.Context) error {
	// 1. Basic connection check
	// Create a fresh connection for validation queries and reuse it for all checks
	conn, err := p.openMetadataConn(ctx)
	if err != nil {
		return fmt.Errorf("postgres connection failed: %w", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	if err := conn.Ping(ctx); err != nil {
		return fmt.Errorf("postgres connection failed: %w", err)
	}

	if !p.useCDC {
		return nil
	}

	// 2. CDC-specific checks
	p.mu.Lock()
	isPooled := p.pooled
	p.mu.Unlock()

	if isPooled {
		return errors.New("CDC requires a direct Postgres connection; PgBouncer transaction/statement mode does not support logical replication. Provide a direct (session-mode) connection string for CDC sources")
	}

	if err := p.checkWALLevel(ctx, conn); err != nil {
		return err
	}

	heldByUs, err := p.checkReplicationStatus(ctx, conn)
	if err != nil {
		return err
	}

	if err := p.checkTrackingTables(ctx, conn); err != nil {
		return err
	}

	// If the replication slot is already active and held by our own application name,
	// we have verified that replication privileges and configuration are working.
	// Skipping probeReplication avoids hits on max_wal_senders and potential hangs
	// during workflow restarts or concurrent test-connection requests.
	if heldByUs {
		return nil
	}

	// Probe replication privileges (uses a short-lived replication connection)
	return p.probeReplication(ctx)
}

func (p *PostgresSource) checkWALLevel(ctx context.Context, conn *pgx.Conn) error {
	var walLevel string
	if err := conn.QueryRow(ctx, "SHOW wal_level").Scan(&walLevel); err != nil {
		return fmt.Errorf("failed to check wal_level: %w", err)
	}
	if walLevel != "logical" {
		return fmt.Errorf("postgres 'wal_level' must be 'logical' for CDC (currently '%s'). Please update postgresql.conf and restart postgres", walLevel)
	}
	return nil
}

func (p *PostgresSource) checkReplicationStatus(ctx context.Context, conn *pgx.Conn) (bool, error) {
	// Check existing replication slot and active_pid. We also fetch the
	// application_name to distinguish between a foreign consumer and our own
	// instances (which use a stable, host-prefixed application_name).
	var active bool
	var activePID *int32
	var holderAppName string
	var heldByUs bool

	err := conn.QueryRow(ctx, `
		SELECT s.active, s.active_pid, COALESCE(a.application_name, '')
		FROM pg_replication_slots s
		LEFT JOIN pg_stat_activity a ON a.pid = s.active_pid
		WHERE s.slot_name = $1`, p.slotName).Scan(&active, &activePID, &holderAppName)

	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return false, fmt.Errorf("failed to check replication slot %q: %w", p.slotName, err)
		}
		// Slot doesn't exist yet, which is acceptable
	} else if active {
		// If the slot is active, we only fail the test connection if it's held by
		// a foreign consumer. If the application_name matches our own, it's either
		// our already-running worker or a reclaimable orphan from a previous run,
		// both of which are acceptable states for a successful test connection.
		if holderAppName != p.appName {
			pid := "unknown"
			if activePID != nil {
				pid = fmt.Sprintf("%d", *activePID)
			}
			return false, fmt.Errorf("replication slot %q is already active (PID %s, application_name %q). Logical replication slots can only have one active consumer", p.slotName, pid, holderAppName)
		}
		heldByUs = true
	}

	// Check existing publication
	var pubExists bool
	err = conn.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_publication WHERE pubname = $1)", p.publicationName).Scan(&pubExists)
	if err != nil {
		return false, fmt.Errorf("failed to check publication %q: %w", p.publicationName, err)
	}
	if !pubExists {
		return false, fmt.Errorf("publication %q does not exist", p.publicationName)
	}

	return heldByUs, nil
}

func (p *PostgresSource) checkTrackingTables(ctx context.Context, conn *pgx.Conn) error {
	for _, table := range p.tables {
		// table may be schema.tablename or just tablename
		parts := strings.Split(table, ".")
		var schema, name string
		if len(parts) == 2 {
			schema = parts[0]
			name = parts[1]
		} else {
			schema = "public"
			name = parts[0]
		}

		var exists bool
		query := "SELECT EXISTS (SELECT 1 FROM pg_catalog.pg_tables WHERE schemaname = $1 AND tablename = $2)"
		if err := conn.QueryRow(ctx, query, schema, name).Scan(&exists); err != nil {
			return fmt.Errorf("failed to check table %q: %w", table, err)
		}
		if !exists {
			return fmt.Errorf("tracking table %q does not exist", table)
		}
	}
	return nil
}

// probeReplication opens (and immediately closes) a replication connection to
// validate privileges. It uses a short sub-deadline so it fails fast rather
// than consuming the caller's entire timeout budget.
func (p *PostgresSource) probeReplication(ctx context.Context) error {
	// Use a very short timeout for probing; if the server is so loaded it can't
	// accept a new replication connection in 3s, the test should fail fast.
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	replCfg, _, err := pgxutil.ParseConfig(p.connString)
	if err != nil {
		return fmt.Errorf("failed to parse connection string: %w", err)
	}
	if replCfg.RuntimeParams == nil {
		replCfg.RuntimeParams = make(map[string]string)
	}
	replCfg.RuntimeParams["replication"] = "database"
	// Set application_name to indicate this is a probe
	replCfg.RuntimeParams["application_name"] = p.appName + "-probe"

	replConn, err := pgx.ConnectConfig(probeCtx, replCfg)
	if err != nil {
		return classifyReplicationError(err, replCfg.User)
	}
	// Do not run SQL on a replication connection; simply close it if successful.
	_ = replConn.Close(probeCtx)
	return nil
}

// classifyReplicationError maps low-level connection failures to actionable
// operator-facing messages.
func classifyReplicationError(err error, user string) error {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		if pgErr.Code == "28P01" {
			return errors.New("replication connection failed: invalid password. Ensure user has replication privileges and correct credentials")
		}
		if pgErr.Code == "28000" {
			return fmt.Errorf("replication connection failed: user does not have replication privileges. Run 'ALTER USER %s REPLICATION'", user)
		}
	}
	return fmt.Errorf("replication connection failed: %w. Ensure 'wal_level' is set to 'logical' in postgresql.conf", err)
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
		// Need a new connection for cleanup. Use the pooler-safe parser so the
		// custom pgbouncer/pool_mode markers are stripped from the DSN.
		conn, err := p.openMetadataConn(context.Background())
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
	// Build a dedicated connection for discovery, independent of p.conn. The
	// pooler-safe parser strips the custom pgbouncer/pool_mode markers that pgx
	// would otherwise reject as unknown startup parameters.
	cfg, _, err := pgxutil.ParseConfig(p.connString)
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
	// Create a new connection for discovery and close it immediately.
	conn, err := p.openMetadataConn(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close(ctx) }()

	rows, err := conn.Query(ctx, "SELECT schemaname || '.' || tablename FROM pg_catalog.pg_tables WHERE schemaname NOT IN ('pg_catalog', 'information_schema')")
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
	// Create a new connection for discovery and close it immediately.
	conn, err := p.openMetadataConn(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close(ctx) }()

	rows, err := conn.Query(ctx, "SELECT slot_name, COALESCE(plugin, ''), COALESCE(slot_type, ''), COALESCE(database, ''), active FROM pg_replication_slots ORDER BY slot_name")
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
	// Create a new connection for discovery and close it immediately.
	conn, err := p.openMetadataConn(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close(ctx) }()

	rows, err := conn.Query(ctx, "SELECT pubname, puballtables FROM pg_publication ORDER BY pubname")
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
		tableRows, err := conn.Query(ctx, "SELECT schemaname || '.' || tablename FROM pg_publication_tables WHERE pubname = $1 ORDER BY 1", pubs[i].Name)
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
	// Create a new connection for discovery and close it immediately.
	conn, err := p.openMetadataConn(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close(ctx) }()

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

	rows, err := conn.Query(ctx, query, table)
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
	// Create a new connection for sampling and close it immediately.
	conn, err := p.openMetadataConn(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close(ctx) }()

	quoted, err := sqlutil.QuoteIdent("pgx", table)
	if err != nil {
		return nil, fmt.Errorf("invalid table name: %w", err)
	}
	rows, err := conn.Query(ctx, fmt.Sprintf("SELECT * FROM %s LIMIT 1", quoted))
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

// snapshotBatchSize bounds how many rows are fetched from the server-side
// cursor per round-trip during a table snapshot, keeping memory usage flat for
// arbitrarily large tables.
const snapshotBatchSize = 1000

func (p *PostgresSource) snapshotTable(ctx context.Context, table string) error {
	quoted, err := sqlutil.QuoteIdent("pgx", table)
	if err != nil {
		return fmt.Errorf("invalid table name %q: %w", table, err)
	}

	// Use a dedicated connection so a large table scan neither blocks lightweight
	// metadata operations (Ping/Discover) on the shared connection nor races on
	// it (a pgx.Conn is not safe for concurrent use).
	conn, err := p.openMetadataConn(ctx)
	if err != nil {
		return fmt.Errorf("failed to open snapshot connection for %q: %w", table, err)
	}
	defer func() { _ = conn.Close(context.Background()) }()

	return p.streamSnapshotCursor(ctx, conn, table, quoted)
}

// streamSnapshotCursor declares a server-side cursor over the table and streams
// it to the message channel in bounded batches.
func (p *PostgresSource) streamSnapshotCursor(ctx context.Context, conn *pgx.Conn, table, quoted string) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin snapshot tx for %q: %w", table, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Cursor name is derived from a UUID (hex only), so it is a safe identifier.
	cursorName := "hermod_snap_" + strings.ReplaceAll(uuid.New().String(), "-", "")
	if _, err := tx.Exec(ctx, fmt.Sprintf("DECLARE %s CURSOR FOR SELECT * FROM %s", cursorName, quoted)); err != nil {
		return fmt.Errorf("failed to declare snapshot cursor for %q: %w", table, err)
	}

	fetchSQL := fmt.Sprintf("FETCH FORWARD %d FROM %s", snapshotBatchSize, cursorName)
	for {
		n, err := p.fetchSnapshotBatch(ctx, tx, table, fetchSQL)
		if err != nil {
			return err
		}
		if n < snapshotBatchSize {
			return nil
		}
	}
}

// fetchSnapshotBatch fetches and emits a single cursor batch, returning the
// number of rows processed.
func (p *PostgresSource) fetchSnapshotBatch(ctx context.Context, tx pgx.Tx, table, fetchSQL string) (int, error) {
	rows, err := tx.Query(ctx, fetchSQL)
	if err != nil {
		return 0, fmt.Errorf("failed to fetch snapshot batch for %q: %w", table, err)
	}
	defer rows.Close()

	fields := rows.FieldDescriptions()
	count := 0
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return count, fmt.Errorf("failed to get values: %w", err)
		}

		record := make(map[string]any, len(fields))
		for i, field := range fields {
			if b, ok := values[i].([]byte); ok {
				record[field.Name] = string(b)
			} else {
				record[field.Name] = values[i]
			}
		}

		if err := p.emitSnapshotRecord(ctx, table, record); err != nil {
			return count, err
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return count, err
	}
	return count, nil
}

// emitSnapshotRecord wraps a snapshot row in a message and delivers it to the
// channel, honoring context cancellation.
func (p *PostgresSource) emitSnapshotRecord(ctx context.Context, table string, record map[string]any) error {
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
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *PostgresSource) ExecuteSQL(ctx context.Context, query string) ([]map[string]any, error) {
	// A pgx.Conn is single-owner and a live pgx.Rows pins the connection for the
	// whole iteration. Using the shared metadata conn (p.conn) and releasing the
	// lock after Query() returns would let a concurrent Ping/DiscoverColumns/Sample/
	// Ack interleave on the same wire, corrupting the protocol and racing on p.conn.
	// Use a dedicated, pooler-safe connection instead so a preview can never race
	// with other metadata calls and is safe behind PgBouncer.
	conn, err := p.openMetadataConn(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to open query connection: %w", err)
	}
	defer func() { _ = conn.Close(context.Background()) }()

	rows, err := conn.Query(ctx, query)
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

		record := make(map[string]any, len(fields))
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
	// A mid-stream failure must surface as an error rather than returning a
	// silently truncated result set.
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}
