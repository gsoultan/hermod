package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pglogrepl"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgproto3"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
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

// buildReplicationAppName derives an instance-unique application_name from the
// host, slot, process PID and a random session ID. Including the PID and session
// ID ensures that concurrent instances (e.g. overlapping restarts or test
// connections) have distinct names and don't accidentally terminate each other,
// while the stable prefix still allows a restarted worker to recognize its
// predecessors.
func buildReplicationAppName(host, slotName string, pid int, sessionID string) string {
	if strings.TrimSpace(host) == "" {
		host = "unknown"
	}
	// Format: hermod-cdc-<host>-<slot>-<pid>-<session>
	// sessionID is typically a short UUID fragment to keep it under 63 chars.
	name := fmt.Sprintf("%s%s-%s-%d-%s", replicationAppNamePrefix, host, slotName, pid, sessionID)
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
	pooled          bool          // Whether connString targets a transaction/statement pooler (PgBouncer)
	persistentSlot  bool          // Whether to keep the slot on Close
	appName         string        // Stable, instance-unique application_name for the replication connection
	pool            *pgxpool.Pool // Shared connection pool for metadata
	replConn        *pgx.Conn     // Replication connection for streaming

	// initialLoad asks for the rows already in the watched tables to be carried
	// across before streaming begins. Off by default: turning it on for an
	// existing workflow would re-read every source table on the next restart.
	initialLoad bool
	// snapshotHoldConn is the replication connection that created the slot and
	// exported the snapshot. The snapshot stays valid only while it is open, so
	// it is held for the backfill and closed straight after.
	snapshotHoldConn *pgx.Conn
	// exportedSnapshot is the snapshot the slot exported when it was created,
	// valid only while replConn stays open. Empty when there is no backfill to
	// run — because none was asked for, or because the slot already existed and
	// a consistent one is no longer obtainable.
	exportedSnapshot string
	typeMap          *pgtype.Map
	relations        map[uint32]*pglogrepl.RelationMessage
	mu               sync.Mutex
	initialized      bool
	lastReceivedLSN  pglogrepl.LSN
	lastAckedLSN     pglogrepl.LSN
	// lastStreamActivity (unix nanos) is when anything last arrived on the
	// replication stream, keepalives included. Read without the mutex by the
	// engine's liveness watcher, so it is atomic.
	lastStreamActivity atomic.Int64
	// lastEmittedLSN is the highest WAL position of a change actually handed to
	// consumers. Compared against lastAckedLSN it says whether this source is
	// still owed acknowledgements, which replication lag cannot: see
	// PendingWork.
	lastEmittedLSN atomic.Uint64
	// walSenderTimeout (nanos) is the server's wal_sender_timeout, read once per
	// stream establishment. It sets the cadence keepalives are promised at, and
	// therefore how long silence must last to mean something. Zero means the
	// server sends no keepalives, so silence proves nothing.
	walSenderTimeout atomic.Int64
	msgChan          chan hermod.Message
	errChan          chan error
	cancel           context.CancelFunc
	initMu           sync.Mutex
	wg               sync.WaitGroup
	query            string
	pollInterval     time.Duration
	lastWatermark    any
	// logMu guards logger only. It is intentionally separate from mu: log() is
	// called from many code paths that already hold mu (init, Close and every
	// *Locked helper). Since mu is a non-reentrant sync.Mutex, having log()
	// acquire mu would self-deadlock and silently freeze the streaming goroutine
	// (the source would appear "online" but never deliver changes). A dedicated
	// lock lets logging run safely regardless of whether mu is held.
	logMu  sync.RWMutex
	logger hermod.Logger
}

func NewPostgresSource(connString, slotName, publicationName string, tables []string, useCDC bool, query string, pollInterval time.Duration) *PostgresSource {
	// Fall back to safe, valid identifiers when the form leaves these empty.
	// Without a valid slot/publication name Postgres cannot create the logical
	// replication slot, so no changes would ever be streamed.
	if strings.TrimSpace(slotName) == "" {
		slotName = defaultSlotName
	}
	if strings.TrimSpace(publicationName) == "" {
		publicationName = defaultPublicationName
	}

	if pollInterval <= 0 {
		pollInterval = 5 * time.Second
	}

	sessionID := uuid.New().String()
	if len(sessionID) > 8 {
		sessionID = sessionID[:8]
	}

	return &PostgresSource{
		connString:      connString,
		slotName:        slotName,
		publicationName: publicationName,
		tables:          tables,
		useCDC:          useCDC,
		query:           query,
		pollInterval:    pollInterval,
		pooled:          pgxutil.IsPooledConnString(connString),
		persistentSlot:  true, // Default to persistent for reliability
		appName:         buildReplicationAppName(hostnameOrUnknown(), slotName, os.Getpid(), sessionID),
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
	err := p.pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_publication WHERE pubname = $1)", p.publicationName).Scan(&exists)
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
	if _, err := p.pool.Exec(ctx, query); err != nil {
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
	err := p.pool.QueryRow(ctx, "SELECT puballtables FROM pg_publication WHERE pubname = $1", p.publicationName).Scan(&pubAllTables)
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
		// Postgres does not allow 'ALTER PUBLICATION ... SET TABLE' for publications
		// created as 'FOR ALL TABLES'. To align with a specific table list we
		// must drop and recreate it. We only do this because the user has
		// explicitly provided a table list.
		p.log("WARN", "Existing publication is FOR ALL TABLES; dropping and recreating to support specific table list", "publication", p.publicationName)
		if _, err := p.pool.Exec(ctx, "DROP PUBLICATION "+quotedPub); err != nil {
			return fmt.Errorf("failed to drop FOR ALL TABLES publication: %w", err)
		}
		return p.createPublication(ctx, quotedPub)
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
	rows, err := p.pool.Query(ctx, "SELECT schemaname, tablename FROM pg_publication_tables WHERE pubname = $1", p.publicationName)
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
	if _, err := p.pool.Exec(ctx, query); err != nil {
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
	_, err = p.pool.Exec(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to create publication with discovered tables: %w", err)
	}
	p.log("INFO", "Created publication with all discovered tables", "publication", p.publicationName)
	return nil
}

// SetInitialLoad asks for a one-time backfill of the watched tables before
// streaming starts.
//
// It happens only when the replication slot is created, which is the source's
// own record of having run before: if the slot exists, changes have been
// streamed from it and the rows are already downstream. That makes the backfill
// once-only without any extra bookkeeping, and means enabling this on a running
// workflow does nothing until the slot is dropped.
func (p *PostgresSource) SetInitialLoad(enabled bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.initialLoad = enabled
}

// runInitialLoad carries the rows already in the watched tables across, reading
// at the snapshot the slot exported when it was created.
//
// A failure is logged rather than returned: the slot exists by this point and is
// already accumulating WAL, so refusing to stream would strand it while solving
// nothing. What is lost is the pre-existing rows, which is exactly what the log
// says.
func (p *PostgresSource) runInitialLoad(ctx context.Context, tables []string) {
	defer func() {
		// Release the snapshot and the connection holding it open. Leaving that
		// connection around would keep a transaction open on the server for the
		// life of the source, which holds back vacuum on every table it can see.
		p.mu.Lock()
		p.exportedSnapshot = ""
		hold := p.snapshotHoldConn
		p.snapshotHoldConn = nil
		p.mu.Unlock()

		if hold != nil {
			closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = hold.Close(closeCtx)
		}
	}()

	if len(tables) == 0 {
		discovered, err := p.DiscoverTables(ctx)
		if err != nil {
			p.log("ERROR", "Initial load could not discover tables; rows already in the "+
				"source will not be carried across", "error", err.Error())
			return
		}
		tables = discovered
	}

	p.log("INFO", "Initial load starting", "tables", strings.Join(tables, ","))
	for _, table := range tables {
		if ctx.Err() != nil {
			return
		}
		if err := p.snapshotTable(ctx, table); err != nil {
			p.log("ERROR", "Initial load failed for a table; its existing rows were not "+
				"carried across, though later changes to it will still stream",
				"table", table, "error", err.Error())
			continue
		}
	}
	p.log("INFO", "Initial load complete; streaming changes from the slot's consistent point")
}

func (p *PostgresSource) ensureReplicationSlot(ctx context.Context) error {
	var exists bool
	err := p.pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_replication_slots WHERE slot_name = $1)", p.slotName).Scan(&exists)
	if err != nil {
		return fmt.Errorf("failed to check if replication slot exists: %w", err)
	}

	if exists {
		// The slot has streamed before, so the rows are already downstream and
		// there is nothing to backfill. This is also why the backfill needs no
		// separate "already done" record.
		return nil
	}

	if p.wantsInitialLoad() {
		err := p.createSlotWithExportedSnapshot(ctx)
		if err == nil {
			return nil
		}
		// Fall through to the ordinary creation below rather than refuse to
		// start. Without the exported snapshot there is no consistent backfill,
		// so none is attempted — streaming a table's changes is still better
		// than not starting at all, and the log says what was lost.
		p.log("WARN", "Could not create the slot with an exported snapshot; "+
			"starting without an initial load, so rows already in the table will not be carried across",
			"slot", p.slotName, "error", err.Error())
	}

	_, err = p.pool.Exec(ctx, "SELECT pg_create_logical_replication_slot($1, 'pgoutput')", p.slotName)
	if err != nil {
		if strings.Contains(err.Error(), "wal_level") {
			return fmt.Errorf("failed to create replication slot: wal_level must be set to 'logical' in postgres.conf: %w", err)
		}
		return fmt.Errorf("failed to create replication slot: %w", err)
	}
	p.log("INFO", "Created replication slot", "slot", p.slotName)
	return nil
}

// validSlotName mirrors what PostgreSQL accepts for a replication slot. The
// replication protocol takes no parameters, so the name is inlined into the
// command and has to be checked rather than escaped.
func validSlotName(name string) bool {
	if name == "" || len(name) > 63 {
		return false
	}
	for _, r := range name {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}

// isSyntaxError reports whether the server rejected the command as malformed,
// which is how a pre-15 server answers the modern option-list spelling and how
// a modern one answers the legacy keyword.
func isSyntaxError(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "42601"
	}
	return strings.Contains(strings.ToLower(err.Error()), "syntax error")
}

func (p *PostgresSource) wantsInitialLoad() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.initialLoad
}

// createSlotWithExportedSnapshot creates the slot over the replication protocol
// so it exports a snapshot, which is what makes a gapless handoff possible.
//
// pg_create_logical_replication_slot(), the SQL function used otherwise, cannot
// export one. Only CREATE_REPLICATION_SLOT on a replication connection can, and
// the snapshot it hands back is valid only while that connection stays open —
// which is why the backfill runs before streaming starts on the same connection,
// and why this is the only moment a consistent backfill is available at all.
//
// The snapshot is taken at the slot's consistent point, so every change after it
// arrives on the stream: no gap, and no duplicate beyond what the delivery
// guarantee already allows.
func (p *PostgresSource) createSlotWithExportedSnapshot(ctx context.Context) error {
	// A connection of its own, not the one used for streaming.
	//
	// Exporting a snapshot holds a transaction open on the connection that
	// created the slot, for as long as the snapshot must stay readable. Reusing
	// that connection to stream afterwards leaves it pinned by that
	// transaction: replication starts, reports itself connected at the
	// consistent point, and then never advances. Streaming keeps its own
	// connection, and this one is closed once the backfill is done.
	conn, err := p.openReplicationConn(ctx)
	if err != nil {
		return fmt.Errorf("replication connection for the exported snapshot: %w", err)
	}
	keep := false
	defer func() {
		if !keep {
			closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = conn.Close(closeCtx)
		}
	}()

	// The command is issued here rather than through pglogrepl's helper, which
	// still emits the pre-15 form ("... pgoutput EXPORT_SNAPSHOT"). PostgreSQL 15
	// replaced that with a parenthesised option list and removed the old
	// keyword, so the helper fails with a syntax error on any current server.
	// Both spellings are tried, newest first.
	if !validSlotName(p.slotName) {
		return fmt.Errorf("slot name %q is not a valid replication slot name; "+
			"only lower-case letters, digits and underscores are allowed", p.slotName)
	}

	res, err := pglogrepl.ParseCreateReplicationSlot(conn.PgConn().Exec(ctx,
		fmt.Sprintf("CREATE_REPLICATION_SLOT %s LOGICAL pgoutput (SNAPSHOT 'export')", p.slotName)))
	if err != nil && isSyntaxError(err) {
		res, err = pglogrepl.ParseCreateReplicationSlot(conn.PgConn().Exec(ctx,
			fmt.Sprintf("CREATE_REPLICATION_SLOT %s LOGICAL pgoutput EXPORT_SNAPSHOT", p.slotName)))
	}
	if err != nil {
		if strings.Contains(err.Error(), "wal_level") {
			return fmt.Errorf("wal_level must be set to 'logical': %w", err)
		}
		return err
	}

	p.mu.Lock()
	p.exportedSnapshot = res.SnapshotName
	p.snapshotHoldConn = conn
	p.mu.Unlock()
	keep = true

	p.log("INFO", "Created replication slot with an exported snapshot for the initial load",
		"slot", p.slotName, "consistent_point", res.ConsistentPoint, "snapshot", res.SnapshotName)
	return nil
}

func (p *PostgresSource) Read(ctx context.Context) (hermod.Message, error) {
	if err := p.init(ctx); err != nil {
		return nil, err
	}

	if !p.useCDC && p.query != "" {
		for {
			select {
			case msg := <-p.msgChan:
				return msg, nil
			case err := <-p.errChan:
				return nil, err
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	} else if !p.useCDC {
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

		// Only a shutdown retires the source. Anything else reconnects.
		//
		// This returned on a nil error too, which retired the source
		// permanently while the workflow was still active: streamLoop exited,
		// teardownStream set initialized = false, and nothing decoded WAL ever
		// again. Read then blocked forever on a message that could not arrive,
		// so the whole pipeline sat idle — worker pool idle, sink writer idle,
		// no errors logged — while pgx's background reader kept draining the
		// socket, advancing sent_lsn and making the source look alive. The only
		// cure was restarting the workflow, which built a new source.
		if ctx.Err() != nil || errors.Is(err, context.Canceled) {
			return
		}
		if err == nil {
			p.log("WARN", "Replication stream ended without an error; reconnecting", "slot", p.slotName)
		} else {
			p.log("WARN", "Replication stream interrupted, reconnecting", "slot", p.slotName, "error", err)
		}
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
		// Ensure closure doesn't hang indefinitely if the connection is wedged.
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = p.replConn.Close(ctx)
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

	// A freshly established stream counts as activity, so the liveness watcher
	// measures silence from now rather than from whenever the previous stream
	// died.
	p.noteStreamActivity()
	p.refreshWalSenderTimeout(ctx)

	p.log("INFO", "Replication stream (re)established", "slot", slotName, "publication", pubName,
		"keepalive_deadline", p.StreamSilenceThreshold().String())
	return nil
}

// noteStreamActivity records that something arrived on the replication stream.
func (p *PostgresSource) noteStreamActivity() {
	p.lastStreamActivity.Store(time.Now().UnixNano())
}

// noteEmitted records the WAL position of a change actually handed to consumers.
func (p *PostgresSource) noteEmitted(lsn pglogrepl.LSN) {
	if lsn == 0 {
		return
	}
	for {
		prev := p.lastEmittedLSN.Load()
		if uint64(lsn) <= prev {
			return
		}
		if p.lastEmittedLSN.CompareAndSwap(prev, uint64(lsn)) {
			return
		}
	}
}

// PendingWork implements hermod.PendingWorkReporter. It reports whether this
// source has handed over changes that were never acknowledged.
//
// Only positions actually delivered count. WAL that arrived and was filtered
// out — a table this workflow does not follow, or traffic in another database
// on the same server — is not work this pipeline owes, even though it shows up
// as replication lag. Conflating the two is what would make an idle workflow on
// a busy server look permanently wedged.
func (p *PostgresSource) PendingWork() (pending bool, known bool) {
	if !p.useCDC {
		return false, false
	}
	p.mu.Lock()
	acked := p.lastAckedLSN
	p.mu.Unlock()
	return p.lastEmittedLSN.Load() > uint64(acked), true
}

// LastStreamActivity implements hermod.StreamLivenessReporter.
func (p *PostgresSource) LastStreamActivity() time.Time {
	ns := p.lastStreamActivity.Load()
	if ns == 0 {
		return time.Time{}
	}
	return time.Unix(0, ns)
}

// streamSilenceFactor multiplies the keepalive cadence to get the deadline. A
// keepalive is due every wal_sender_timeout/2, so three missed keepalives in a
// row is the threshold — tolerant of one lost packet and one slow scheduler,
// intolerant of a stream that has actually stopped.
const streamSilenceFactor = 3

// StreamSilenceThreshold implements hermod.StreamLivenessReporter. It is derived
// from the server's own wal_sender_timeout rather than assumed, because a server
// configured with a long timeout sends keepalives correspondingly rarely and a
// hardcoded deadline would declare a healthy stream dead.
func (p *PostgresSource) StreamSilenceThreshold() time.Duration {
	if !p.useCDC {
		return 0
	}
	timeout := time.Duration(p.walSenderTimeout.Load())
	if timeout <= 0 {
		// wal_sender_timeout = 0 disables keepalives entirely, so silence
		// carries no information and the check must stay off.
		return 0
	}
	return streamSilenceFactor * (timeout / 2)
}

// refreshWalSenderTimeout reads the server's keepalive cadence. A failure leaves
// the previous value in place and, on a first attempt, leaves the liveness check
// disabled — an unknown cadence is not grounds for declaring a stream dead.
func (p *PostgresSource) refreshWalSenderTimeout(ctx context.Context) {
	var ms int64
	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err := p.pool.QueryRow(queryCtx,
		`SELECT setting::bigint FROM pg_settings WHERE name = 'wal_sender_timeout'`).Scan(&ms)
	if err != nil {
		p.log("WARN", "Could not read wal_sender_timeout; stream liveness detection is disabled for this stream",
			"error", err.Error())
		return
	}
	p.walSenderTimeout.Store(int64(time.Duration(ms) * time.Millisecond))
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
		// Anything at all — WAL data or a bare keepalive — proves the walsender
		// is still serving this stream. Recorded before dispatch so a message
		// that fails to parse still counts as the stream being alive.
		p.noteStreamActivity()

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
// flushPosition is the WAL position this source may safely confirm to Postgres.
//
// Confirming a position tells the server every change below it is dealt with and
// its WAL may be discarded. Get it wrong in one direction and acknowledged-but-
// undelivered changes are destroyed with no way to replay them; get it wrong in
// the other and the slot pins WAL on the source database forever.
//
// The acknowledged position is always safe. Beyond it, the received position is
// safe too — but only when everything this source ever handed to the pipeline
// has come back acknowledged. The gap between the last delivered change and the
// last received one is WAL the pipeline was never given: changes to tables
// outside the publication, activity in other databases on the same instance,
// autovacuum. Nothing downstream can be waiting on data it was never sent, so
// holding that WAL protects nothing and costs the source database its disk.
//
// When something is outstanding, no such argument is available: a delivered
// message that has not been acknowledged might still be in flight, buffered in a
// sink, or waiting on a retry, and confirming past it would discard the only
// copy. So the position stays pinned at the acknowledgement.
func (p *PostgresSource) flushPosition() pglogrepl.LSN {
	p.mu.Lock()
	received := p.lastReceivedLSN
	acked := p.lastAckedLSN
	p.mu.Unlock()

	if !p.useCDC {
		return acked
	}

	// Anything delivered and not yet acknowledged pins the position.
	if p.lastEmittedLSN.Load() > uint64(acked) {
		return acked
	}
	if received > acked {
		return received
	}
	return acked
}

func (p *PostgresSource) sendStandbyStatus(ctx context.Context, conn *pgx.Conn) error {
	p.mu.Lock()
	write := p.lastReceivedLSN
	p.mu.Unlock()
	flush := p.flushPosition()
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
		return p.dispatch(ctx, currentLSN, p.handleInsert(currentLSN, lm))
	case *pglogrepl.UpdateMessage:
		return p.dispatch(ctx, currentLSN, p.handleUpdate(currentLSN, lm))
	case *pglogrepl.DeleteMessage:
		return p.dispatch(ctx, currentLSN, p.handleDelete(currentLSN, lm))
	default:
		return nil
	}
}

// dispatchBlockedWarnAfter is how long a handover to the consumer may stall
// before it is reported. Backpressure is normal and this must not fire on it;
// what it exists to catch is the indefinite case.
const dispatchBlockedWarnAfter = 15 * time.Second

// dispatch delivers a change message to consumers, respecting cancellation.
//
// The handover deliberately blocks: dropping a decoded change here would lose
// data that Postgres has already handed us and will not resend. But blocking is
// also what makes a wedged pipeline invisible — this call sits inside the
// replication loop, so while it waits, consumeStream cannot send standby status
// updates. confirmed_flush_lsn then freezes and the source database retains WAL
// indefinitely, with nothing logged. Observed at 21 MB and climbing on an
// otherwise "running" workflow.
//
// So: still never drop, but stop being silent about it.
func (p *PostgresSource) dispatch(ctx context.Context, lsn pglogrepl.LSN, msg hermod.Message) error {
	if msg == nil {
		return nil
	}

	// Fast path: hand over without allocating a timer.
	select {
	case p.msgChan <- msg:
		p.noteEmitted(lsn)
		return nil
	case <-ctx.Done():
		message.ReleaseMessage(msg)
		return ctx.Err()
	default:
	}

	warn := time.NewTimer(dispatchBlockedWarnAfter)
	defer warn.Stop()
	started := time.Now()
	warned := false

	for {
		select {
		case p.msgChan <- msg:
			p.noteEmitted(lsn)
			if warned {
				p.log("INFO", "CDC handover resumed; the consumer is draining again",
					"blocked_for", time.Since(started).String())
			}
			return nil
		case <-ctx.Done():
			message.ReleaseMessage(msg)
			return ctx.Err()
		case <-warn.C:
			// Report once, then keep waiting: repeating this every interval
			// would bury the log under one line per stalled message.
			p.log("ERROR", "CDC stalled: the consumer is not accepting changes, so replication cannot be acknowledged",
				"blocked_for", time.Since(started).String(),
				"slot", p.slotName,
				"hint", "the source database is retaining WAL for this slot; check sink health")
			warned = true
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

func (p *PostgresSource) pollLoop(ctx context.Context) {
	p.log("INFO", "Starting pollLoop", "query", p.query, "interval", p.pollInterval)
	ticker := time.NewTicker(p.pollInterval)
	defer ticker.Stop()

	// Initial poll
	if err := p.poll(ctx); err != nil {
		p.log("ERROR", "Initial poll failure", "error", err)
		select {
		case p.errChan <- err:
		case <-ctx.Done():
			return
		default:
		}
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := p.poll(ctx); err != nil {
				p.log("ERROR", "Poll failure", "error", err)
				select {
				case p.errChan <- err:
				case <-ctx.Done():
					return
				default:
				}
			}
		}
	}
}

func (p *PostgresSource) poll(ctx context.Context) error {
	if err := p.ensureConn(ctx); err != nil {
		return err
	}

	p.mu.Lock()
	watermark := p.lastWatermark
	p.mu.Unlock()

	var rows pgx.Rows
	var err error
	if watermark != nil {
		rows, err = p.pool.Query(ctx, p.query, watermark)
	} else {
		// If query has a placeholder but we have no watermark, try with 0
		// or just execute without args if it doesn't have placeholders.
		// For simplicity, we try to pass 0 if we suspect a placeholder.
		if strings.Contains(p.query, "$1") {
			rows, err = p.pool.Query(ctx, p.query, 0)
		} else {
			rows, err = p.pool.Query(ctx, p.query)
		}
	}

	if err != nil {
		return fmt.Errorf("poll query failed: %w", err)
	}
	defer rows.Close()

	fields := rows.FieldDescriptions()
	count := 0
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return fmt.Errorf("failed to get values: %w", err)
		}

		record := make(map[string]any, len(fields))
		for i, field := range fields {
			val := values[i]
			if b, ok := val.([]byte); ok {
				record[field.Name] = string(b)
			} else {
				record[field.Name] = val
			}

			// Track watermark: if column name is 'id' or matches a configured id_field
			if strings.ToLower(field.Name) == "id" {
				p.mu.Lock()
				p.lastWatermark = val
				p.mu.Unlock()
			}
		}

		msg := message.AcquireMessage()
		afterJSON, _ := json.Marshal(message.SanitizeMap(record))
		msg.SetOperation(hermod.OpCreate)
		msg.SetAfter(afterJSON)
		msg.SetMetadata("source", "postgres-polling")

		if id, ok := record["id"]; ok {
			msg.SetID(fmt.Sprintf("%v", id))
		} else {
			msg.SetID(uuid.New().String())
		}

		select {
		case p.msgChan <- msg:
			count++
		case <-ctx.Done():
			message.ReleaseMessage(msg)
			return ctx.Err()
		}
	}

	if count > 0 {
		p.log("DEBUG", "Polled records", "count", count, "last_watermark", p.lastWatermark)
	} else {
		p.log("DEBUG", "Polled 0 records", "query", p.query, "watermark", watermark)
	}
	return rows.Err()
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
	if p.pool != nil {
		p.mu.Unlock()
		return nil
	}
	p.mu.Unlock()

	// Use shared pooler for metadata connections.
	pool, err := pgxutil.DefaultPooler.Get(ctx, p.connString)
	if err != nil {
		return fmt.Errorf("failed to get shared postgres pool: %w", err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.pooled = pgxutil.IsPooledConnString(p.connString)
	p.pool = pool
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

// openReplicationConn dials a fresh replication connection. The caller owns it.
func (p *PostgresSource) openReplicationConn(ctx context.Context) (*pgx.Conn, error) {
	connConfig, pooled, err := pgxutil.ParseConfig(p.connString)
	if err != nil {
		return nil, fmt.Errorf("failed to parse connection string for replication: %w", err)
	}
	if pooled {
		return nil, errors.New("CDC requires a direct Postgres connection; PgBouncer " +
			"transaction/statement mode does not support logical replication")
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

	return pgx.ConnectConfig(ctx, connConfig)
}

func (p *PostgresSource) ensureConnNoLock(ctx context.Context) error {
	if p.pool != nil {
		return nil
	}
	return errors.New("connection pool not established (call ensureConn first)")
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
		if p.query != "" {
			if p.cancel != nil {
				p.cancel()
			}
			pollCtx, cancel := context.WithCancel(context.Background())
			p.cancel = cancel
			p.wg.Go(func() {
				p.pollLoop(pollCtx)
			})
		}
		p.mu.Unlock()
		return nil
	}

	p.mu.Unlock()

	p.log("INFO", "Initializing PostgresSource", "slot", p.slotName, "publication", p.publicationName)

	// Quicker check: does slot already exist?
	var slotExists bool
	_ = p.pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_replication_slots WHERE slot_name = $1)", p.slotName).Scan(&slotExists)
	if slotExists {
		// If it exists, try to reclaim it immediately if possible
		p.reclaimSlotIfStale(ctx)
	}

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
	backfill := p.exportedSnapshot
	tables := append([]string(nil), p.tables...)
	p.wg.Go(func() {
		// The backfill runs before streaming and on the same goroutine, so the
		// rows already in the tables reach the pipeline ahead of any change to
		// them. It cannot run inline in Read(): it delivers into the same
		// channel Read drains, so the first call would block on a full buffer
		// and never return to empty it.
		if backfill != "" {
			p.runInitialLoad(streamCtx, tables)
		}
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
		// Use p.pool for metadata operations. ensurePublication/ReplicationSlot
		// don't hold the lock but use p.pool. We assume it's stable because of initMu.
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
	pool := p.pool
	slotName := p.slotName
	if pool == nil {
		p.mu.Unlock()
		return
	}
	p.mu.Unlock()

	var activePID *int32
	var holderAlive bool
	var holderAppName string
	err := pool.QueryRow(ctx,
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

	if _, err := pool.Exec(ctx, "SELECT pg_terminate_backend($1)", *activePID); err != nil {
		p.log("WARN", "Failed to terminate stale slot holder",
			"slot", slotName, "active_pid", *activePID, "error", err)
		return
	}
	p.waitSlotReleased(ctx)
}

// isOwnOrphanLocked reports whether a live slot holder is this source's own
// previous walsender, left behind by an ungraceful restart, rather than a
// foreign live consumer.
//
// It implements a multi-stage check to avoid "mutual kill" loops between live
// instances:
//  1. If the holder's application_name doesn't match our prefix, it's foreign.
//  2. If it matches our prefix and host, it's a predecessor or neighbor.
//  3. If it's a DIFFERENT Hermod process PID, we only kill it if that process
//     is actually dead (checked via Signal(0)).
//  4. If it's the SAME Hermod process PID, it's a leaked connection from an
//     earlier instance in the same process; we only kill it if its instance UUID
//     differs from ours.
//
// Callers must hold p.mu.
func (p *PostgresSource) isOwnOrphanLocked(activePID int32, holderAppName string) bool {
	if !strings.HasPrefix(holderAppName, replicationAppNamePrefix) {
		return false
	}

	// 1. Check if it matches our host (the first part after prefix)
	myHost := hostnameOrUnknown()
	prefixWithHost := replicationAppNamePrefix + myHost + "-"
	if !strings.HasPrefix(holderAppName, prefixWithHost) {
		// Matches prefix but not our host. Could be another worker on a
		// different machine; don't touch it.
		return false
	}

	// 2. Exact match check (including PID and Session)
	if holderAppName == p.appName {
		// It's US (same host, PID, and Session).
		// If we already have an active replication connection, and it's NOT
		// the one we are looking at, then we somehow leaked a connection.
		if p.replConn != nil && !p.replConn.IsClosed() {
			if pgConn := p.replConn.PgConn(); pgConn != nil && int32(pgConn.PID()) == activePID {
				return false // It's our own CURRENT connection. Don't kill!
			}
		}
		// It has our name but it's not our current connection. Orphan!
		return true
	}

	// 3. Different instance on the same host (different PID or Session).
	// Format: hermod-cdc-<host>-<slot>-<pid>-<session>
	// We check if the process that owns it is still alive.
	suffix := strings.TrimPrefix(holderAppName, prefixWithHost)
	parts := strings.Split(suffix, "-")
	// Slot might contain dashes, so we should look from the end or parse carefully.
	// Actually, our buildReplicationAppName uses host-slot-pid-session.
	// Since host part is already stripped, we have slot-pid-session.
	if len(parts) >= 2 {
		pidStr := parts[len(parts)-2]
		pid, err := strconv.Atoi(pidStr)
		if err == nil {
			if isPIDAlive(pid) {
				// The owner process is still running. It might be an
				// overlapping restart or a concurrent worker. Back off!
				return false
			}
			// Predecessor process is dead. Safe to reclaim.
			return true
		}
	}

	// Fallback: if we can't be sure, don't kill a live backend.
	return false
}

func isPIDAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Unix, Signal(0) checks existence without actually sending a signal.
	err = process.Signal(syscall.Signal(0))
	return err == nil
}

// waitSlotReleased polls until the slot is no longer active or the
// slotReleaseTimeout elapses. It does not hold p.mu during I/O or sleep.
func (p *PostgresSource) waitSlotReleased(ctx context.Context) {
	p.mu.Lock()
	pool := p.pool
	slotName := p.slotName
	p.mu.Unlock()

	if pool == nil {
		return
	}

	deadline := time.Now().Add(slotReleaseTimeout)
	for time.Now().Before(deadline) {
		var active bool
		err := pool.QueryRow(ctx,
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
	if p.pool == nil {
		return
	}
	var lsnText *string
	err := p.pool.QueryRow(ctx,
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
	if err := p.ensureConn(ctx); err != nil {
		return err
	}
	return p.pool.Ping(ctx)
}

func (p *PostgresSource) IsReady(ctx context.Context) error {
	start := time.Now()
	// 1. Basic connection check
	if err := p.ensureConn(ctx); err != nil {
		return p.wrapError(fmt.Errorf("postgres connection failed: %w", err), time.Since(start))
	}

	if err := p.pool.Ping(ctx); err != nil {
		return p.wrapError(fmt.Errorf("postgres connection failed: %w", err), time.Since(start))
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

	// Consolidate metadata checks (WAL level, slot, publication) into a single query
	var walLevel string
	var slotExists bool
	var slotActive bool
	var slotPID *int32
	var slotAppName *string
	var pubExists bool

	query := `
		SELECT 
			(SELECT setting FROM pg_settings WHERE name = 'wal_level'),
			(SELECT EXISTS (SELECT 1 FROM pg_replication_slots WHERE slot_name = $1)),
			(SELECT active FROM pg_replication_slots WHERE slot_name = $1),
			(SELECT active_pid FROM pg_replication_slots WHERE slot_name = $1),
			(SELECT application_name FROM pg_stat_activity a WHERE a.pid = (SELECT active_pid FROM pg_replication_slots WHERE slot_name = $1)),
			(SELECT EXISTS (SELECT 1 FROM pg_publication WHERE pubname = $2))`

	err := p.pool.QueryRow(ctx, query, p.slotName, p.publicationName).Scan(
		&walLevel, &slotExists, &slotActive, &slotPID, &slotAppName, &pubExists)

	if err != nil {
		return p.wrapError(fmt.Errorf("failed to check postgres metadata: %w", err), time.Since(start))
	}

	if walLevel != "logical" {
		return fmt.Errorf("postgres 'wal_level' must be 'logical' for CDC (currently %q). Please update postgresql.conf and restart postgres", walLevel)
	}

	heldByUs := false
	if slotActive {
		appName := ""
		if slotAppName != nil {
			appName = *slotAppName
		}
		if appName != p.appName {
			pid := "unknown"
			if slotPID != nil {
				pid = fmt.Sprintf("%d", *slotPID)
			}
			return fmt.Errorf("replication slot %q is already active (PID %s, application_name %q). Logical replication slots can only have one active consumer", p.slotName, pid, appName)
		}
		heldByUs = true
	}

	if !pubExists {
		return fmt.Errorf("publication %q does not exist", p.publicationName)
	}

	if err := p.checkTrackingTables(ctx); err != nil {
		return p.wrapError(err, time.Since(start))
	}

	// If the replication slot is already active and held by our own application name,
	// we have verified that replication privileges and configuration are working.
	if heldByUs {
		return nil
	}

	// Probe replication privileges (uses a short-lived replication connection)
	if err := p.probeReplication(ctx); err != nil {
		return p.wrapError(err, time.Since(start))
	}
	return nil
}

func (p *PostgresSource) wrapError(err error, duration time.Duration) error {
	if err == nil {
		return nil
	}

	// If the connection took a long time, add a human-friendly recommendation.
	if duration > 3*time.Second || errors.Is(err, context.DeadlineExceeded) {
		rec := ""
		if !p.pooled {
			rec = "\n\nRecommendation: The connection is taking a long time (%v). If your database is behind a proxy like PgBouncer, try adding 'pgbouncer=true' or 'pool_mode=transaction' to your connection string. This enables the simple query protocol which is much faster with connection poolers."
		} else {
			rec = "\n\nRecommendation: The connection to your PgBouncer pooler is slow (%v). Check if the pool is exhausted or if the backend database is under heavy load. You may need to increase the 'max_client_conn' or 'default_pool_size' in pgbouncer.ini."
		}
		return fmt.Errorf("%w"+rec, err, duration.Round(time.Millisecond))
	}

	return err
}

func (p *PostgresSource) checkTrackingTables(ctx context.Context) error {
	if len(p.tables) == 0 {
		return nil
	}

	// Split tables into schema and name parts for the query
	schemas := make([]string, len(p.tables))
	names := make([]string, len(p.tables))
	for i, table := range p.tables {
		parts := strings.Split(table, ".")
		if len(parts) == 2 {
			schemas[i] = parts[0]
			names[i] = parts[1]
		} else {
			schemas[i] = "public"
			names[i] = parts[0]
		}
	}

	// Use a single query with ANY to check all tables at once
	query := `
		SELECT schemaname, tablename 
		FROM pg_catalog.pg_tables 
		WHERE (schemaname, tablename) IN (
			SELECT unnest($1::text[]), unnest($2::text[])
		)`

	rows, err := p.pool.Query(ctx, query, schemas, names)
	if err != nil {
		return fmt.Errorf("failed to check tracking tables: %w", err)
	}
	defer rows.Close()

	found := make(map[string]bool)
	for rows.Next() {
		var s, t string
		if err := rows.Scan(&s, &t); err != nil {
			return err
		}
		found[s+"."+t] = true
	}

	for i, table := range p.tables {
		key := schemas[i] + "." + names[i]
		if !found[key] {
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
	// metadata pool (p.pool) may have been accessed by lightweight
	// operations such as Ping (test connection), DiscoverTables/Columns or
	// replication-slot/publication discovery.
	wasInitialized := p.initialized || (p.useCDC && p.slotName != "")
	p.initialized = false

	if p.cancel != nil {
		p.cancel()
	}

	persistent := p.persistentSlot
	slotName := p.slotName
	publicationName := p.publicationName

	// Close connections to unblock ReceiveMessage if context cancel doesn't
	p.closeReplConnLocked()
	// Do not close the shared pool; it is managed by the DefaultPooler.
	p.mu.Unlock()

	// Wait for streamLoop to finish
	p.wg.Wait()

	// Only attempt slot/publication cleanup when CDC streaming was actually
	// initialized OR when a slot was requested; otherwise no replication slot was created by this source.
	if wasInitialized && !persistent && slotName != "" {
		p.log("INFO", "Cleaning up non-persistent replication slot and publication", "slot", slotName, "publication", publicationName)
		// Use the pool for cleanup with a bounded timeout.
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := p.ensureConn(cleanupCtx); err == nil {
			_, _ = p.pool.Exec(cleanupCtx, "SELECT pg_drop_replication_slot($1)", slotName)
		}
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastReceivedLSN = 0
	p.lastAckedLSN = 0
	p.relations = make(map[uint32]*pglogrepl.RelationMessage)

	p.replConn = nil
	p.pool = nil

	return nil
}

func (p *PostgresSource) DiscoverDatabases(ctx context.Context) ([]string, error) {
	start := time.Now()
	// Use the shared pooler. If the current connection string points to a
	// non-existent database, we try to connect to 'postgres' or 'template1'
	// to list available databases.
	pool, err := pgxutil.DefaultPooler.Get(ctx, p.connString)
	if err != nil {
		// Fallback for discovery if the specific DB doesn't exist yet
		cfg, _, parseErr := pgxutil.ParsePoolConfig(p.connString)
		if parseErr == nil {
			// Try connecting to a default maintenance database
			for _, db := range []string{"postgres", "template1"} {
				cfg.ConnConfig.Database = db
				// We don't use the cache for this fallback to avoid polluting it with maintenance DBs
				tempPool, err := pgxpool.NewWithConfig(ctx, cfg)
				if err == nil {
					defer tempPool.Close()
					return p.listDatabases(ctx, tempPool, start)
				}
			}
		}
		return nil, p.wrapError(fmt.Errorf("connect for discovery: %w", err), time.Since(start))
	}

	return p.listDatabases(ctx, pool, start)
}

func (p *PostgresSource) listDatabases(ctx context.Context, exec any, start time.Time) ([]string, error) {
	// We need Query, which is on pool but not on hermod.SQLExecutor
	// Since we know it's either *pgxpool.Pool or *pgx.Conn, we can use a local helper
	type queryer interface {
		Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	}
	q, ok := exec.(queryer)
	if !ok {
		return nil, errors.New("unsupported executor for database listing")
	}

	rows, err := q.Query(ctx, "SELECT datname FROM pg_database WHERE datistemplate = false ORDER BY 1")
	if err != nil {
		return nil, p.wrapError(fmt.Errorf("failed to query databases: %w", err), time.Since(start))
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
	start := time.Now()
	if err := p.ensureConn(ctx); err != nil {
		return nil, p.wrapError(err, time.Since(start))
	}

	rows, err := p.pool.Query(ctx, "SELECT schemaname || '.' || tablename FROM pg_catalog.pg_tables WHERE schemaname NOT IN ('pg_catalog', 'information_schema')")
	if err != nil {
		return nil, p.wrapError(fmt.Errorf("failed to query tables: %w", err), time.Since(start))
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
	start := time.Now()
	if err := p.ensureConn(ctx); err != nil {
		return nil, p.wrapError(err, time.Since(start))
	}

	rows, err := p.pool.Query(ctx, "SELECT slot_name, COALESCE(plugin, ''), COALESCE(slot_type, ''), COALESCE(database, ''), active FROM pg_replication_slots ORDER BY slot_name")
	if err != nil {
		return nil, p.wrapError(fmt.Errorf("failed to query replication slots: %w", err), time.Since(start))
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
	start := time.Now()
	if err := p.ensureConn(ctx); err != nil {
		return nil, p.wrapError(err, time.Since(start))
	}

	rows, err := p.pool.Query(ctx, "SELECT pubname, puballtables FROM pg_publication ORDER BY pubname")
	if err != nil {
		return nil, p.wrapError(fmt.Errorf("failed to query publications: %w", err), time.Since(start))
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
		tableRows, err := p.pool.Query(ctx, "SELECT schemaname || '.' || tablename FROM pg_publication_tables WHERE pubname = $1 ORDER BY 1", pubs[i].Name)
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

	return p.discoverColumnsInternal(ctx, p.pool, table)
}

func (p *PostgresSource) GetLag(ctx context.Context) (uint64, error) {
	if !p.useCDC {
		return 0, nil
	}

	if err := p.ensureConn(ctx); err != nil {
		return 0, err
	}

	var lag *int64
	query := `SELECT pg_wal_lsn_diff(pg_current_wal_lsn(), confirmed_flush_lsn) 
			  FROM pg_replication_slots WHERE slot_name = $1`
	err := p.pool.QueryRow(ctx, query, p.slotName).Scan(&lag)
	if err != nil {
		return 0, err
	}
	if lag == nil || *lag < 0 {
		return 0, nil
	}
	return uint64(*lag), nil
}

type pgQueryer interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func (p *PostgresSource) discoverColumnsInternal(ctx context.Context, conn pgQueryer, table string) ([]hermod.ColumnInfo, error) {
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
	if err := p.ensureConn(ctx); err != nil {
		return nil, err
	}

	quoted, err := sqlutil.QuoteIdent("pgx", table)
	if err != nil {
		return nil, fmt.Errorf("invalid table name: %w", err)
	}
	rows, err := p.pool.Query(ctx, fmt.Sprintf("SELECT * FROM %s LIMIT 1", quoted))
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
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = conn.Close(closeCtx)
	}()

	return p.streamSnapshotCursor(ctx, conn, table, quoted)
}

// streamSnapshotCursor declares a server-side cursor over the table and streams
// it to the message channel in bounded batches.
func (p *PostgresSource) streamSnapshotCursor(ctx context.Context, conn *pgx.Conn, table, quoted string) error {
	cols, err := p.discoverColumnsInternal(ctx, conn, table)
	if err != nil {
		return fmt.Errorf("failed to discover columns for snapshot of %q: %w", table, err)
	}

	var colNames []string
	var pkCols []string
	for _, c := range cols {
		quoted, _ := sqlutil.QuoteIdent("postgres", c.Name)
		colNames = append(colNames, quoted)
		if c.IsPK {
			pkCols = append(pkCols, c.Name)
		}
	}

	colList := "*"
	if len(colNames) > 0 {
		colList = strings.Join(colNames, ", ")
	}

	// A backfill that is part of an initial load reads at the slot's consistent
	// point, so it sees the database exactly as of the moment streaming begins:
	// nothing committed before it is missed, and nothing after it is read twice.
	// REPEATABLE READ is required — SET TRANSACTION SNAPSHOT is rejected at READ
	// COMMITTED — and it has to be the first statement in the transaction.
	snapshot := p.currentExportedSnapshot()

	txOpts := pgx.TxOptions{}
	if snapshot != "" {
		txOpts.IsoLevel = pgx.RepeatableRead
	}
	tx, err := conn.BeginTx(ctx, txOpts)
	if err != nil {
		return fmt.Errorf("failed to begin snapshot tx for %q: %w", table, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if snapshot != "" {
		if _, err := tx.Exec(ctx, "SET TRANSACTION SNAPSHOT "+quoteLiteral(snapshot)); err != nil {
			return fmt.Errorf("failed to bind the snapshot for %q: %w", table, err)
		}
	}

	// Cursor name is derived from a UUID (hex only), so it is a safe identifier.
	cursorName := "hermod_snap_" + strings.ReplaceAll(uuid.New().String(), "-", "")
	if _, err := tx.Exec(ctx, fmt.Sprintf("DECLARE %s CURSOR FOR SELECT %s FROM %s", cursorName, colList, quoted)); err != nil {
		return fmt.Errorf("failed to declare snapshot cursor for %q: %w", table, err)
	}

	fetchSQL := fmt.Sprintf("FETCH FORWARD %d FROM %s", snapshotBatchSize, cursorName)
	for {
		n, err := p.fetchSnapshotBatch(ctx, tx, table, fetchSQL, pkCols)
		if err != nil {
			return err
		}
		if n < snapshotBatchSize {
			return nil
		}
	}
}

func (p *PostgresSource) currentExportedSnapshot() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exportedSnapshot
}

// quoteLiteral renders a string as a SQL literal. Snapshot names come from the
// server, but SET TRANSACTION SNAPSHOT takes no parameters, so the value has to
// be inlined and must be escaped rather than trusted.
func quoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// fetchSnapshotBatch fetches and emits a single cursor batch, returning the
// number of rows processed.
func (p *PostgresSource) fetchSnapshotBatch(ctx context.Context, tx pgx.Tx, table, fetchSQL string, pkCols []string) (int, error) {
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

		if err := p.emitSnapshotRecord(ctx, table, record, pkCols); err != nil {
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
func (p *PostgresSource) emitSnapshotRecord(ctx context.Context, table string, record map[string]any, pkCols []string) error {
	afterJSON, _ := json.Marshal(message.SanitizeMap(record))

	msg := message.AcquireMessage()

	// Use deterministic ID if primary keys are available
	if len(pkCols) > 0 {
		var pkVals []string
		for _, pk := range pkCols {
			pkVals = append(pkVals, fmt.Sprintf("%v", record[pk]))
		}
		msg.SetID(fmt.Sprintf("snapshot-%s-%s", table, strings.Join(pkVals, "-")))
	} else {
		// Fallback to non-deterministic but unique ID
		msg.SetID(fmt.Sprintf("snapshot-%s-%d-%s", table, time.Now().UnixNano(), uuid.New().String()))
	}

	msg.SetOperation(hermod.OpSnapshot)
	msg.SetTable(table)
	msg.SetAfter(afterJSON)
	msg.SetMetadata("source", "postgres")
	msg.SetMetadata("snapshot", "true")

	select {
	case p.msgChan <- msg:
		return nil
	case <-ctx.Done():
		message.ReleaseMessage(msg)
		return ctx.Err()
	}
}

func (p *PostgresSource) ExecuteSQL(ctx context.Context, query string) ([]map[string]any, error) {
	if err := p.ensureConn(ctx); err != nil {
		return nil, err
	}

	rows, err := p.pool.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	fields := rows.FieldDescriptions()
	var results []map[string]any
	for rows.Next() {
		if len(results) >= sqlutil.DefaultMaxRows {
			break
		}
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
