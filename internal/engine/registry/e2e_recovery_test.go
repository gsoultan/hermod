package registry

import (
	"context"
	"database/sql"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	hermod "github.com/gsoultan/Hermod"
	"github.com/gsoultan/Hermod/internal/factory"
	"github.com/gsoultan/Hermod/internal/storage"
	sqlstorage "github.com/gsoultan/Hermod/internal/storage/sql"
	_ "github.com/jackc/pgx/v5/stdlib"
)

const (
	recoverySourceDSN   = "postgres://postgres:postgres@localhost:5432/hermod_test_source?sslmode=disable"
	recoverySinkDSN     = "postgres://postgres:postgres@localhost:5432/hermod_test_sink?sslmode=disable"
	recoveryMetadataDSN = "postgres://postgres:postgres@localhost:5432/hermod_metadata?sslmode=disable"
)

// gateSink is a sink whose far end can be taken away and given back.
//
// Blocking inside Write is what a sink outage actually looks like to the engine:
// the write neither succeeds nor fails, it just never completes, and the
// pipeline fills up behind it. Returning an error instead would exercise the
// retry path, which is a different (and already working) story.
type gateSink struct {
	mu      sync.Mutex
	blocked bool

	written atomic.Int64
	rows    sync.Map // order_ref -> struct{}
}

func (s *gateSink) block(v bool) {
	s.mu.Lock()
	s.blocked = v
	s.mu.Unlock()
}

func (s *gateSink) isBlocked() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.blocked
}

func (s *gateSink) Write(ctx context.Context, msg hermod.Message) error {
	for s.isBlocked() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
	if msg != nil {
		if ref, ok := msg.Data()["order_ref"]; ok {
			s.rows.Store(fmt.Sprintf("%v", ref), struct{}{})
		}
	}
	s.written.Add(1)
	return nil
}

func (s *gateSink) distinct() int {
	n := 0
	s.rows.Range(func(any, any) bool { n++; return true })
	return n
}

func (s *gateSink) Ping(context.Context) error { return nil }
func (s *gateSink) Close() error               { return nil }

type recoveryHarness struct {
	reg      *Registry
	sink     *gateSink
	sourceDB *sql.DB
	wf       storage.Workflow
	slot     string

	restarts atomic.Int64
	logger   *captureLogger
}

// newRecoveryHarness stands up a workflow that reads real CDC from
// hermod_test_source through the real registry, engine and supervisor, and
// writes to a sink the test can take offline.
// seedRecoveryTables creates the source table these tests replicate from, plus
// the sink table they land in.
//
// REPLICA IDENTITY FULL because the pipeline reads before-images; the default
// only publishes the primary key, and the sink writes would be missing columns.
func seedRecoveryTables(t *testing.T, sourceDB *sql.DB) {
	t.Helper()
	ctx := context.Background()

	mustSeed := func(db *sql.DB, stmts ...string) {
		t.Helper()
		for _, q := range stmts {
			if _, err := db.ExecContext(ctx, q); err != nil {
				t.Fatalf("seeding %q: %v", q, err)
			}
		}
	}

	mustSeed(sourceDB,
		`CREATE TABLE IF NOT EXISTS orders (
			id SERIAL PRIMARY KEY,
			order_ref TEXT,
			customer_code TEXT,
			amount NUMERIC
		)`,
		`ALTER TABLE orders REPLICA IDENTITY FULL`,
		`TRUNCATE orders`,
		// customers is the "unmonitored" table in the retention tests: traffic
		// against it must not pin WAL, which only means anything if the table
		// exists and is genuinely outside the publication.
		`CREATE TABLE IF NOT EXISTS customers (
			id SERIAL PRIMARY KEY,
			customer_code TEXT,
			customer_name TEXT,
			region TEXT
		)`,
		`TRUNCATE customers`,
	)

	sinkDB, err := sql.Open("pgx", recoverySinkDSN)
	if err != nil {
		t.Fatalf("open sink db: %v", err)
	}
	t.Cleanup(func() { _ = sinkDB.Close() })
	if err := sinkDB.PingContext(ctx); err != nil {
		t.Skipf("sink database unreachable: %v", err)
	}
	mustSeed(sinkDB,
		`CREATE TABLE IF NOT EXISTS orders_enriched (
			id TEXT PRIMARY KEY,
			data JSONB
		)`,
		`TRUNCATE orders_enriched`,
	)
}

func newRecoveryHarness(t *testing.T, stallThreshold time.Duration) *recoveryHarness {
	t.Helper()
	ctx := context.Background()

	db, err := sql.Open("pgx", recoveryMetadataDSN)
	if err != nil {
		t.Fatalf("open metadata db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ms := sqlstorage.NewSQLStorage(db, "pgx")
	if err := ms.Init(ctx); err != nil {
		t.Fatalf("init metadata storage: %v", err)
	}

	stamp := time.Now().UnixNano()
	slot := fmt.Sprintf("sl_recovery_%d", stamp)
	pub := fmt.Sprintf("pb_recovery_%d", stamp)

	sourceDB, err := sql.Open("pgx", recoverySourceDSN)
	if err != nil {
		t.Fatalf("open source db: %v", err)
	}
	t.Cleanup(func() { _ = sourceDB.Close() })

	if err := sourceDB.PingContext(ctx); err != nil {
		t.Skipf("source database unreachable: %v", err)
	}

	dropStaleRecoverySlots(t, sourceDB)

	// Create the tables rather than assume them. These referenced `orders` and
	// `orders_enriched` without ever creating either, so on a machine without
	// that hand-prepared fixture the publication could not be created, the CDC
	// source never attached, and the test failed 30s later on "the replication
	// slot never became active" -- which reads like a replication bug and is
	// not one.
	seedRecoveryTables(t, sourceDB)

	h := &recoveryHarness{
		sink:     &gateSink{},
		sourceDB: sourceDB,
		slot:     slot,
		logger:   &captureLogger{},
	}

	reg := NewRegistry(ms)
	t.Cleanup(func() { reg.Close() })
	reg.SetLogger(h.logger)

	// Detect quickly so the test is minutes rather than tens of minutes; the
	// production default is 60s and the logic under test is identical.
	cfg := reg.config
	cfg.StallThreshold = stallThreshold
	cfg.StatusInterval = time.Second
	cfg.StreamSilenceInterval = time.Second
	reg.SetConfig(cfg)

	// Names as well as IDs vary per run. The metadata database has a unique
	// constraint on name, and cleanup deletes by ID — so a run interrupted
	// partway leaves a row behind that every later run then collides with, for
	// good. That is a permanently poisoned database from one Ctrl-C, and it
	// presents as a duplicate-key error in a test about sink outages.
	sourceID := fmt.Sprintf("src-recovery-%d", stamp)
	sinkID := fmt.Sprintf("snk-recovery-%d", stamp)
	wfID := fmt.Sprintf("wf-recovery-%d", stamp)

	src := storage.Source{
		ID:   sourceID,
		Name: fmt.Sprintf("Recovery CDC Source %d", stamp),
		Type: "postgres",
		Config: map[string]string{
			"connection_string": recoverySourceDSN,
			"use_cdc":           "true",
			"slot_name":         slot,
			"publication_name":  pub,
			"tables":            "orders",
		},
	}
	if err := ms.CreateSource(ctx, src); err != nil {
		t.Fatalf("create source: %v", err)
	}
	t.Cleanup(func() { _ = ms.DeleteSource(context.Background(), sourceID) })

	snk := storage.Sink{
		ID:   sinkID,
		Name: fmt.Sprintf("Recovery Sink %d", stamp),
		Type: "postgres",
		Config: map[string]string{
			"connection_string": recoverySinkDSN,
			"table":             "orders_enriched",
		},
	}
	if err := ms.CreateSink(ctx, snk); err != nil {
		t.Fatalf("create sink: %v", err)
	}
	t.Cleanup(func() { _ = ms.DeleteSink(context.Background(), sinkID) })

	// The sink is the only stubbed component: everything upstream of it — the
	// replication slot, the CDC decoder, the buffer, the router, the watchdogs
	// and the supervisor — is the real thing.
	reg.sinkFactory = func(cfg factory.SinkConfig) (hermod.Sink, error) {
		return h.sink, nil
	}

	h.wf = storage.Workflow{
		ID:     wfID,
		Name:   fmt.Sprintf("Recovery E2E %d", stamp),
		Active: true,
		Nodes: []storage.WorkflowNode{
			{ID: "src-1", Type: "source", RefID: sourceID},
			{ID: "snk-1", Type: "sink", RefID: sinkID},
		},
		Edges: []storage.WorkflowEdge{{SourceID: "src-1", TargetID: "snk-1"}},
	}
	if err := ms.CreateWorkflow(ctx, h.wf); err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	t.Cleanup(func() { _ = ms.DeleteWorkflow(context.Background(), wfID) })

	// Observe the supervisor without replacing it: count rebuilds, then perform
	// the real one.
	reg.rebuildWorkflow = func(rctx context.Context, id string, wf storage.Workflow) error {
		h.restarts.Add(1)
		return reg.restartWorkflowEngine(rctx, id, wf)
	}

	h.reg = reg
	t.Cleanup(func() {
		_ = reg.StopEngine(context.Background(), wfID)
		h.dropSlot(t)
	})
	return h
}

// walsenderPID returns the backend process serving this slot's replication
// stream, or 0 when nothing is attached.
func (h *recoveryHarness) walsenderPID(t *testing.T) int {
	t.Helper()
	var pid *int
	err := h.sourceDB.QueryRowContext(t.Context(),
		`SELECT active_pid FROM pg_replication_slots WHERE slot_name = $1`, h.slot).Scan(&pid)
	if err != nil || pid == nil {
		return 0
	}
	return *pid
}

// effectiveSilenceThreshold reports the deadline the running engine's source
// actually derived from the server, which is the only figure the watchdog acts
// on.
func (h *recoveryHarness) effectiveSilenceThreshold(t *testing.T) time.Duration {
	t.Helper()
	h.reg.mu.RLock()
	ae, ok := h.reg.engines[h.wf.ID]
	h.reg.mu.RUnlock()
	if !ok || ae == nil || ae.engine == nil {
		return 0
	}
	lr, ok := ae.engine.GetSource().(hermod.StreamLivenessReporter)
	if !ok {
		t.Fatal("the engine's source does not report stream liveness: a silent replication stream can never be detected")
	}
	return lr.StreamSilenceThreshold()
}

// dropStaleRecoverySlots removes replication slots left behind by earlier runs
// of this test.
//
// The harness drops its own slot on cleanup, but an interrupted run — a
// cancelled test, a timeout, a Ctrl-C — never reaches cleanup, and the slot
// survives. A replication slot holds WAL until it is dropped, so they
// accumulate: nine of them had built up on the development database, pinning up
// to 1.8 GB of WAL each, and the first thing that failed was an unrelated
// retention test rather than anything that created them.
//
// Only inactive slots matching this test's own prefix are dropped. An active
// one belongs to a run happening right now, and nothing else in the system
// names slots this way.
func dropStaleRecoverySlots(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.QueryContext(context.Background(),
		`SELECT slot_name FROM pg_replication_slots
		 WHERE slot_name LIKE 'sl\_recovery\_%' AND NOT active`)
	if err != nil {
		t.Logf("could not list replication slots: %v", err)
		return
	}
	stale := func() []string {
		defer rows.Close()
		var names []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err == nil {
				names = append(names, name)
			}
		}
		if err := rows.Err(); err != nil {
			t.Logf("reading replication slots: %v", err)
		}
		return names
	}()

	for _, name := range stale {
		if _, err := db.ExecContext(context.Background(),
			`SELECT pg_drop_replication_slot($1)`, name); err != nil {
			t.Logf("could not drop the stale slot %s: %v", name, err)
			continue
		}
		t.Logf("dropped replication slot %s, left by an interrupted run", name)
	}
}

func (h *recoveryHarness) dropSlot(t *testing.T) {
	t.Helper()
	// The slot holds WAL on the source database until it is gone.
	for range 10 {
		_, err := h.sourceDB.ExecContext(context.Background(),
			`SELECT pg_drop_replication_slot($1) WHERE EXISTS
			 (SELECT 1 FROM pg_replication_slots WHERE slot_name = $1 AND NOT active)`, h.slot)
		if err == nil {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Logf("replication slot %s was not dropped; drop it by hand if it lingers", h.slot)
}

func (h *recoveryHarness) insertOrders(t *testing.T, prefix string, n int) []string {
	t.Helper()
	refs := make([]string, 0, n)
	for i := range n {
		ref := fmt.Sprintf("%s-%d", prefix, i)
		if _, err := h.sourceDB.ExecContext(context.Background(),
			`INSERT INTO orders (order_ref, customer_code, amount) VALUES ($1, $2, $3)`,
			ref, "CUST-RECOVERY", 10+i); err != nil {
			t.Fatalf("insert order %s: %v", ref, err)
		}
		refs = append(refs, ref)
	}
	return refs
}

func (h *recoveryHarness) slotLagBytes(t *testing.T) int64 {
	t.Helper()
	var lag *int64
	err := h.sourceDB.QueryRowContext(t.Context(),
		`SELECT pg_wal_lsn_diff(pg_current_wal_lsn(), confirmed_flush_lsn)
		 FROM pg_replication_slots WHERE slot_name = $1`, h.slot).Scan(&lag)
	if err != nil || lag == nil {
		return -1
	}
	return *lag
}

func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Logf("timed out after %s waiting for %s", timeout, what)
	return false
}

// This is the incident, reproduced end to end against a real replication slot.
//
// A sink stops completing writes. The pipeline fills, stops delivering, and
// keeps reporting itself healthy. Previously the only cure was an operator
// noticing and restarting the workflow by hand; the point of the supervisor is
// that nobody has to. What must hold afterwards is not just "it restarted" but
// "every row arrived" — recovery that loses data is not recovery.
func TestE2ERecovery_SinkOutageIsDetectedAndHealed(t *testing.T) {
	requireIntegrationInfra(t)

	const stallThreshold = 5 * time.Second
	h := newRecoveryHarness(t, stallThreshold)

	// Take the sink away before anything is produced, so no row can get through.
	h.sink.block(true)

	// The stall clock starts when the engine does, not when rows are written, so
	// this is the baseline the detection latency has to be measured against.
	engineStarted := time.Now()
	if err := h.reg.StartWorkflow(h.wf.ID, h.wf); err != nil {
		t.Fatalf("start workflow: %v", err)
	}

	// Give the replication stream a moment to attach before writing.
	if !waitFor(t, 30*time.Second, "the replication slot to become active", func() bool {
		var active bool
		err := h.sourceDB.QueryRowContext(t.Context(),
			`SELECT active FROM pg_replication_slots WHERE slot_name = $1`, h.slot).Scan(&active)
		return err == nil && active
	}) {
		t.Fatal("the CDC source never attached to its replication slot")
	}

	const rows = 25
	refs := h.insertOrders(t, "outage", rows)

	// 1. The stall must be detected and handed to the supervisor.
	if !waitFor(t, 90*time.Second, "the stall to be detected and the workflow rebuilt", func() bool {
		return h.restarts.Load() > 0
	}) {
		t.Fatalf("a wedged pipeline was never restarted: restarts=%d, lag=%d bytes, log=%v",
			h.restarts.Load(), h.slotLagBytes(t), h.logger.lines)
	}

	detected := time.Since(engineStarted)
	t.Logf("stall detected and workflow rebuilt %s after engine start (threshold %s)",
		detected.Round(time.Millisecond), stallThreshold)

	// A rebuild sooner than the threshold cannot have come from the stall
	// watchdog, and would mean this test passes for the wrong reason.
	if detected < stallThreshold {
		t.Errorf("rebuild happened %s after engine start, sooner than the %s stall threshold: something other than the watchdog triggered it; log=%v",
			detected, stallThreshold, h.logger.lines)
	}

	// The watchdog must have said so in its own words, with the elapsed time.
	if !h.logger.contains("Pipeline stalled") {
		t.Errorf("the watchdog never reported the stall; log=%v", h.logger.lines)
	}

	// 2. The reason must be on the process log, or an operator learns nothing.
	if !h.logger.contains("stalled") {
		t.Errorf("the stall was never reported on the process log; log=%v", h.logger.lines)
	}

	// 3. Retained WAL proves the source was not acknowledged for undelivered
	//    rows: this is what makes the replay in step 4 possible.
	if lag := h.slotLagBytes(t); lag <= 0 {
		t.Errorf("slot lag = %d, want > 0 while the sink was down: undelivered rows may have been acknowledged", lag)
	}

	// 4. Give the sink back. Everything must replay from the slot.
	h.sink.block(false)

	if !waitFor(t, 120*time.Second, "all rows to arrive after recovery", func() bool {
		return h.sink.distinct() >= rows
	}) {
		t.Fatalf("only %d/%d distinct rows arrived after recovery: data was acknowledged but never delivered",
			h.sink.distinct(), len(refs))
	}

	t.Logf("recovered: restarts=%d, distinct rows delivered=%d/%d, writes=%d",
		h.restarts.Load(), h.sink.distinct(), rows, h.sink.written.Load())
}

// The counterpart, and the one that protects production: a workflow with
// nothing to do must never be restarted, however much WAL its source database
// is generating for other reasons. An automatic recovery that fires on healthy
// pipelines is worse than none.
func TestE2ERecovery_IdleWorkflowIsNotRestarted(t *testing.T) {
	requireIntegrationInfra(t)

	h := newRecoveryHarness(t, 3*time.Second)

	if err := h.reg.StartWorkflow(h.wf.ID, h.wf); err != nil {
		t.Fatalf("start workflow: %v", err)
	}

	if !waitFor(t, 30*time.Second, "the replication slot to become active", func() bool {
		var active bool
		err := h.sourceDB.QueryRowContext(t.Context(),
			`SELECT active FROM pg_replication_slots WHERE slot_name = $1`, h.slot).Scan(&active)
		return err == nil && active
	}) {
		t.Fatal("the CDC source never attached to its replication slot")
	}

	// Generate WAL the workflow does not follow. This is the ordinary state of
	// any shared database server, and it drives pg_current_wal_lsn() — and
	// therefore the slot's reported lag — steadily upward.
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			case <-time.After(100 * time.Millisecond):
				_, _ = h.sourceDB.ExecContext(context.Background(),
					`INSERT INTO customers (customer_code, customer_name, region) VALUES ($1, $2, $3)
					 ON CONFLICT DO NOTHING`,
					fmt.Sprintf("NOISE-%d", time.Now().UnixNano()), "unmonitored", "nowhere")
			}
		}
	}()
	defer func() { close(stop); <-done }()

	// Sample throughout rather than only at the end. Retained WAL is now
	// released once nothing is outstanding, so a single reading taken after the
	// fact can legitimately be zero — but the guard under test is only
	// exercised while lag is actually non-zero, so the peak is what matters.
	var peakLag int64
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if lag := h.slotLagBytes(t); lag > peakLag {
			peakLag = lag
		}
		time.Sleep(500 * time.Millisecond)
	}

	if n := h.restarts.Load(); n != 0 {
		t.Fatalf("an idle, healthy workflow was restarted %d times because its source database had unrelated traffic; peak lag=%d bytes; log=%v",
			n, peakLag, h.logger.lines)
	}

	// Without real retained WAL this test proves nothing: it would pass just as
	// well if lag reporting were broken, which is exactly how it passed before
	// the wrapper chain was fixed.
	if peakLag <= 0 {
		t.Fatalf("slot lag never rose above zero, so no retained WAL was ever reported and the false-positive guard was not exercised")
	}
	t.Logf("idle workflow left alone for 30s, peak retained WAL %d bytes", peakLag)
}

// signalWalsender sends a signal to the backend process serving the replication
// stream, from inside the database container.
func signalWalsender(t *testing.T, sig string, pid int) error {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "container", "exec", "postgres-dev", "kill", sig, strconv.Itoa(pid))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("kill %s %d: %w (%s)", sig, pid, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// The wedge the engine's own counters cannot see.
//
// SIGSTOP on the walsender freezes the server side of the replication stream
// without closing the socket: the connection stays open, the slot stays active,
// pg_replication_slots still reports it attached, and the source's readiness
// check — which runs on a separate pooled connection — keeps returning healthy.
// Nothing is queued in the engine, nothing is in flight, and no message is
// un-acknowledged, so every progress-based signal reads "idle and fine".
//
// The only evidence left is that a stream promised to deliver a keepalive every
// wal_sender_timeout/2 has delivered nothing at all.
func TestE2ERecovery_SilentWalsenderIsDetected(t *testing.T) {
	requireIntegrationInfra(t)
	if _, err := exec.LookPath("container"); err != nil {
		t.Skip("needs the `container` CLI to signal the postgres-dev backend")
	}

	h := newRecoveryHarness(t, time.Hour) // progress watchdog off: this is not its wedge

	// Shorten the server's keepalive cadence so the derived silence deadline
	// (3 x wal_sender_timeout/2) is seconds rather than minutes. ALTER SYSTEM
	// plus a reload applies to sessions opened afterwards regardless of role
	// defaults, which ALTER ROLE does not reliably do here. The code path under
	// test is identical; only the clock is faster.
	if _, err := h.sourceDB.ExecContext(context.Background(), `ALTER SYSTEM SET wal_sender_timeout = '10s'`); err != nil {
		t.Skipf("cannot shorten wal_sender_timeout: %v", err)
	}
	if _, err := h.sourceDB.ExecContext(context.Background(), `SELECT pg_reload_conf()`); err != nil {
		t.Skipf("cannot reload postgres config: %v", err)
	}
	t.Cleanup(func() {
		_, _ = h.sourceDB.ExecContext(context.Background(), `ALTER SYSTEM RESET wal_sender_timeout`)
		_, _ = h.sourceDB.ExecContext(context.Background(), `SELECT pg_reload_conf()`)
	})

	if err := h.reg.StartWorkflow(h.wf.ID, h.wf); err != nil {
		t.Fatalf("start workflow: %v", err)
	}

	var pid int
	if !waitFor(t, 30*time.Second, "the replication slot to attach", func() bool {
		pid = h.walsenderPID(t)
		return pid > 0
	}) {
		t.Fatal("the CDC source never attached to its replication slot")
	}

	// Always release the process, even if an assertion fails: a stopped
	// walsender holds the slot and retains WAL indefinitely.
	stopped := false
	t.Cleanup(func() {
		if stopped {
			_ = signalWalsender(t, "-CONT", pid)
		}
	})

	if err := signalWalsender(t, "-STOP", pid); err != nil {
		t.Skipf("cannot stop the walsender: %v", err)
	}
	stopped = true
	t.Logf("walsender pid %d stopped; the stream is now open but unserved", pid)

	// Ask the running source what deadline it actually derived, rather than
	// assuming the GUC change reached it. Waiting on an assumed deadline is how
	// this test previously reported a failure that was really a setup problem.
	deadline := h.effectiveSilenceThreshold(t)
	if deadline <= 0 {
		t.Fatal("the source reported no stream-silence deadline, so the check is disabled and a dead stream would never be noticed")
	}
	t.Logf("effective stream-silence deadline: %s", deadline)

	if !waitFor(t, 2*deadline+30*time.Second, "the silent stream to be detected", func() bool {
		return h.restarts.Load() > 0
	}) {
		t.Fatalf("a replication stream that stopped being served was never detected: restarts=%d, log=%v",
			h.restarts.Load(), h.logger.lines)
	}

	if !h.logger.contains("gone silent") {
		t.Errorf("the silence was not reported in terms an operator can act on; log=%v", h.logger.lines)
	}

	if err := signalWalsender(t, "-CONT", pid); err != nil {
		t.Logf("could not resume walsender %d: %v", pid, err)
	}
	stopped = false

	t.Logf("silent stream detected and workflow rebuilt: restarts=%d", h.restarts.Load())
}

// insertNoise writes to a table this workflow does not follow, which is what
// every shared database server does continuously.
func (h *recoveryHarness) insertNoise(t *testing.T, n int) {
	t.Helper()
	for i := range n {
		if _, err := h.sourceDB.ExecContext(context.Background(),
			`INSERT INTO customers (customer_code, customer_name, region) VALUES ($1, $2, $3)
			 ON CONFLICT DO NOTHING`,
			fmt.Sprintf("NOISE-%d-%d", time.Now().UnixNano(), i), "unmonitored", "nowhere"); err != nil {
			t.Fatalf("insert noise: %v", err)
		}
	}
}

// A replication slot pins WAL on the source database until the consumer
// confirms a position past it. Hermod only ever confirmed positions it had
// acknowledged a message for, so WAL produced by anything it does not
// follow — other tables, other databases, autovacuum — was retained for the
// life of the workflow. With max_slot_wal_keep_size at its -1 default that ends
// as a full disk on someone else's primary, not as an error here.
func TestE2ERetention_UnmonitoredTrafficDoesNotPinWAL(t *testing.T) {
	requireIntegrationInfra(t)

	h := newRecoveryHarness(t, time.Hour) // not a stall test

	if err := h.reg.StartWorkflow(h.wf.ID, h.wf); err != nil {
		t.Fatalf("start workflow: %v", err)
	}
	if !waitFor(t, 30*time.Second, "the replication slot to attach", func() bool {
		return h.walsenderPID(t) > 0
	}) {
		t.Fatal("the CDC source never attached to its replication slot")
	}

	// Produce WAL the workflow does not follow.
	for range 5 {
		h.insertNoise(t, 200)
		time.Sleep(500 * time.Millisecond)
	}
	grown := h.slotLagBytes(t)
	if grown <= 0 {
		t.Skipf("no retained WAL accumulated (lag=%d); nothing to prove", grown)
	}
	t.Logf("retained WAL after unmonitored traffic: %d bytes", grown)

	// Standby status updates go out every 10s, so give a couple of rounds.
	released := waitFor(t, 60*time.Second, "the slot to release WAL it was never going to deliver", func() bool {
		return h.slotLagBytes(t) < grown/2
	})
	after := h.slotLagBytes(t)
	if !released {
		t.Fatalf("retained WAL stayed at %d bytes (was %d): the slot is pinning WAL for changes this workflow was never given, and it will keep doing so for as long as the workflow runs",
			after, grown)
	}
	t.Logf("retained WAL released: %d -> %d bytes", grown, after)
}

// The safety half of the same change, and the one that must never regress:
// while the pipeline is holding delivered-but-unacknowledged changes, the slot
// must not be advanced past them. Confirming there would tell Postgres to
// discard the only copy of data that was never written anywhere.
func TestE2ERetention_UnacknowledgedWorkStillPinsWAL(t *testing.T) {
	requireIntegrationInfra(t)

	h := newRecoveryHarness(t, time.Hour) // no restarts during this test

	// Sink is down: everything delivered stays unacknowledged.
	h.sink.block(true)

	if err := h.reg.StartWorkflow(h.wf.ID, h.wf); err != nil {
		t.Fatalf("start workflow: %v", err)
	}
	if !waitFor(t, 30*time.Second, "the replication slot to attach", func() bool {
		return h.walsenderPID(t) > 0
	}) {
		t.Fatal("the CDC source never attached to its replication slot")
	}

	const rows = 20
	h.insertOrders(t, "pinned", rows)

	// Wait past several standby status updates.
	time.Sleep(25 * time.Second)

	if lag := h.slotLagBytes(t); lag <= 0 {
		t.Fatalf("slot lag = %d while %d delivered rows were unacknowledged: the slot was advanced past data that has not been written anywhere, and it can no longer be replayed",
			lag, rows)
	}

	// And it must all still arrive once the sink returns.
	h.sink.block(false)
	if !waitFor(t, 120*time.Second, "the pinned rows to be delivered", func() bool {
		return h.sink.distinct() >= rows
	}) {
		t.Fatalf("only %d/%d rows arrived: retained WAL did not replay", h.sink.distinct(), rows)
	}
	t.Logf("all %d rows delivered after the sink returned", h.sink.distinct())
}
