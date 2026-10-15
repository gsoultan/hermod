// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

//go:build integration

package mysql

// XA two-phase commit against a real MySQL.
//
// The unit tests pin identifier handling; none of them can say whether the
// server behaves the way the sink assumes — that a prepared XA transaction is
// invisible, survives the connection that prepared it going away, and can be
// resolved from a fresh one. These assert that against XA RECOVER, the view an
// operator would check, rather than trusting the sink's own account.
//
//	docker run -d --name mysql-xa -p 3307:3306 -e MYSQL_ROOT_PASSWORD=root \
//	  -e MYSQL_DATABASE=hermod_test mysql:8.4
//	HERMOD_INTEGRATION=1 MYSQL_DSN='root:root@tcp(127.0.0.1:3307)/hermod_test' \
//	  go test -tags=integration -run XA ./pkg/comm/sink/mysql/

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	gomysql "github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/engine/twopc"
)

func xaDB(t *testing.T) (string, *sql.DB) {
	t.Helper()
	dsn := bulkDSN(t)
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.PingContext(t.Context()); err != nil {
		t.Fatalf("MYSQL_DSN is not reachable: %v", err)
	}
	return dsn, db
}

// recoveredXIDs is XA RECOVER filtered to Hermod's format ID — what an
// operator would look at, independent of the sink under test.
func recoveredXIDs(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), "XA RECOVER")
	if err != nil {
		t.Fatalf("XA RECOVER: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var formatID, gtridLen, bqualLen int64
		var data []byte
		if err := rows.Scan(&formatID, &gtridLen, &bqualLen, &data); err != nil {
			t.Fatalf("scan XA RECOVER: %v", err)
		}
		if formatID == xaFormatID {
			out = append(out, string(data[:gtridLen]))
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("XA RECOVER rows: %v", err)
	}
	return out
}

func isRecovered(t *testing.T, db *sql.DB, txID string) bool {
	t.Helper()
	for _, id := range recoveredXIDs(t, db) {
		if id == txID {
			return true
		}
	}
	return false
}

// rollbackLeftovers rolls back any Hermod XA transaction still prepared when
// the test ends. Register it after the table's own cleanup: cleanups run in
// reverse, and a prepared transaction holds a metadata lock that would make the
// DROP TABLE wait forever.
func rollbackLeftovers(t *testing.T, db *sql.DB) {
	t.Helper()
	t.Cleanup(func() {
		for _, id := range recoveredXIDs(t, db) {
			lit, err := xidLiteral(id)
			if err != nil {
				continue
			}
			if _, err := db.ExecContext(context.Background(), "XA ROLLBACK "+lit); err != nil {
				t.Errorf("cleanup: could not roll back leftover XA transaction %s: %v", id, err)
			}
		}
	})
}

// prepareRows opens an XA transaction on a fresh sink, writes ids, prepares,
// and closes the sink — the process side going away after its vote.
func prepareRows(t *testing.T, dsn, table string, ids []int64) string {
	t.Helper()
	ctx := context.Background()
	s := sinkFor(dsn, table)
	txID := uuid.NewString()
	if err := s.BeginNamed(ctx, txID); err != nil {
		t.Fatalf("BeginNamed: %v", err)
	}
	if err := s.WriteBatch(ctx, insertMsgs(t, ids, "xa")); err != nil {
		t.Fatalf("WriteBatch inside XA: %v", err)
	}
	got, err := s.Prepare(ctx, txID)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if got != txID {
		t.Fatalf("Prepare returned %q, want the supplied %q", got, txID)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return txID
}

func TestXAPreflightAcceptsARealServer(t *testing.T) {
	dsn, _ := xaDB(t)
	s := sinkFor(dsn, "unused")
	defer s.Close()
	if err := s.PreflightTwoPhaseCommit(context.Background()); err != nil {
		t.Fatalf("Preflight rejected a correctly configured server: %v", err)
	}
}

// An account without XA_RECOVER_ADMIN can prepare and commit but cannot list
// what is in doubt, so a stranded transaction would be invisible to it. Refused
// at start-up, by name.
func TestXAPreflightRefusesAnAccountThatCannotRunXARecover(t *testing.T) {
	dsn, db := xaDB(t)
	ctx := context.Background()
	user := "hermod_xa_lowpriv"
	cfg, err := gomysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	for _, stmt := range []string{
		"DROP USER IF EXISTS '" + user + "'@'%'",
		"CREATE USER '" + user + "'@'%' IDENTIFIED BY 'lowpriv'",
		"GRANT ALL ON `" + cfg.DBName + "`.* TO '" + user + "'@'%'",
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Skipf("cannot create a low-privilege user (%v); skipping", err)
		}
	}
	t.Cleanup(func() { _, _ = db.ExecContext(context.Background(), "DROP USER IF EXISTS '"+user+"'@'%'") })

	cfg.User, cfg.Passwd = user, "lowpriv"
	s := sinkFor(cfg.FormatDSN(), "unused")
	defer s.Close()
	err = s.PreflightTwoPhaseCommit(ctx)
	if err == nil || !strings.Contains(err.Error(), "XA_RECOVER_ADMIN") {
		t.Fatalf("Preflight for an account that cannot run XA RECOVER = %v, want a refusal naming XA_RECOVER_ADMIN", err)
	}
}

// Prepare → the preparing connection is gone → commit from a fresh sink.
func TestXAPrepareThenCommitFromAFreshConnection(t *testing.T) {
	dsn, db := xaDB(t)
	table := freshTable(t, db, "xa_commit")
	rollbackLeftovers(t, db)

	txID := prepareRows(t, dsn, table, []int64{1, 2})

	if !isRecovered(t, db, txID) {
		t.Fatalf("XA RECOVER does not list %s after Prepare and disconnect: the vote did not survive", txID)
	}
	if got := rowsOf(t, db, table); len(got) != 0 {
		t.Fatalf("a prepared, uncommitted XA transaction is visible: %v", got)
	}

	fresh := sinkFor(dsn, table)
	defer fresh.Close()
	if err := fresh.CommitPrepared(context.Background(), txID); err != nil {
		t.Fatalf("CommitPrepared from a fresh connection: %v", err)
	}
	if got := rowsOf(t, db, table); len(got) != 2 {
		t.Errorf("after CommitPrepared the table has %v, want 2 rows", got)
	}
	if isRecovered(t, db, txID) {
		t.Errorf("%s is still in XA RECOVER after commit; it is holding locks", txID)
	}
}

func TestXAPrepareThenRollbackFromAFreshConnection(t *testing.T) {
	dsn, db := xaDB(t)
	table := freshTable(t, db, "xa_rollback")
	rollbackLeftovers(t, db)

	txID := prepareRows(t, dsn, table, []int64{1, 2})
	if !isRecovered(t, db, txID) {
		t.Fatalf("XA RECOVER does not list %s after Prepare", txID)
	}

	fresh := sinkFor(dsn, table)
	defer fresh.Close()
	if err := fresh.RollbackPrepared(context.Background(), txID); err != nil {
		t.Fatalf("RollbackPrepared: %v", err)
	}
	if got := rowsOf(t, db, table); len(got) != 0 {
		t.Errorf("after RollbackPrepared the table has %v, want none", got)
	}
	if isRecovered(t, db, txID) {
		t.Errorf("%s is still in XA RECOVER after rollback", txID)
	}

	// Recovery may run twice; the second rollback finds nothing and must succeed.
	if err := fresh.RollbackPrepared(context.Background(), txID); err != nil {
		t.Errorf("a second RollbackPrepared of the same XID = %v, want nil", err)
	}
}

// The coordinator records a name before anything is prepared under it, so a
// crash in between leaves a name with no transaction. Rolling that back must
// succeed, or recovery would retry it forever.
func TestXARollingBackAnXIDThatWasNeverPreparedSucceeds(t *testing.T) {
	dsn, _ := xaDB(t)
	s := sinkFor(dsn, "unused")
	defer s.Close()
	if err := s.RollbackPrepared(context.Background(), uuid.NewString()); err != nil {
		t.Fatalf("RollbackPrepared of an unknown XID = %v, want nil", err)
	}
}

// Committing an XID the server does not know is an error, as on PostgreSQL:
// reporting success would claim a durability nothing provides.
func TestXACommittingAnUnknownXIDIsAnError(t *testing.T) {
	dsn, _ := xaDB(t)
	s := sinkFor(dsn, "unused")
	defer s.Close()
	err := s.CommitPrepared(context.Background(), uuid.NewString())
	if !isXAUnknown(err) {
		t.Fatalf("CommitPrepared of an unknown XID = %v, want MySQL error 1397", err)
	}
}

// Rollback before Prepare discards the writes and leaves nothing in doubt.
func TestXARollbackBeforePrepareDiscardsTheWrites(t *testing.T) {
	dsn, db := xaDB(t)
	table := freshTable(t, db, "xa_abandon")
	rollbackLeftovers(t, db)
	ctx := context.Background()

	s := sinkFor(dsn, table)
	defer s.Close()
	txID := uuid.NewString()
	if err := s.BeginNamed(ctx, txID); err != nil {
		t.Fatalf("BeginNamed: %v", err)
	}
	if err := s.WriteBatch(ctx, insertMsgs(t, []int64{1}, "xa")); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}
	if err := s.Rollback(ctx); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if got := rowsOf(t, db, table); len(got) != 0 {
		t.Errorf("rows survived a rollback: %v", got)
	}
	if isRecovered(t, db, txID) {
		t.Errorf("an abandoned XA transaction is in XA RECOVER")
	}

	// And the sink is usable again on the ordinary path.
	if err := s.WriteBatch(ctx, insertMsgs(t, []int64{2}, "plain")); err != nil {
		t.Fatalf("ordinary WriteBatch after an XA rollback: %v", err)
	}
	if got := rowsOf(t, db, table); len(got) != 1 {
		t.Errorf("after an ordinary write the table has %v, want 1 row", got)
	}
}

// A batch large enough for the multi-row INSERT path, inside XA, into a table
// the sink has to create. Table DDL implicitly commits, which MySQL refuses
// while an XA transaction is ACTIVE, so this fails unless DDL runs outside it.
func TestXAWritesABulkBatchIntoATableTheSinkCreates(t *testing.T) {
	dsn, db := xaDB(t)
	table := "xa_created"
	drop := func() { _, _ = db.ExecContext(context.Background(), "DROP TABLE IF EXISTS "+table) }
	drop()
	t.Cleanup(drop)
	rollbackLeftovers(t, db)

	ids := make([]int64, bulkMinRows+5)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	s := NewMySQLSink(dsn, table, bulkIntegrationMappings(), false, "", "", "", "auto", false, false)
	if mode := s.classifyBatch(insertMsgs(t, ids, "probe")); mode != bulkModeMultiValues {
		t.Fatalf("the batch does not take the bulk path (mode %v); the test would not cover it", mode)
	}
	_ = s.Close()

	ctx := context.Background()
	s = NewMySQLSink(dsn, table, bulkIntegrationMappings(), false, "", "", "", "auto", false, false)
	defer s.Close()
	txID := uuid.NewString()
	if err := s.BeginNamed(ctx, txID); err != nil {
		t.Fatalf("BeginNamed: %v", err)
	}
	if err := s.WriteBatch(ctx, insertMsgs(t, ids, "xa")); err != nil {
		t.Fatalf("WriteBatch into a table the sink creates, inside XA: %v", err)
	}
	if _, err := s.Prepare(ctx, txID); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := s.CommitPrepared(ctx, txID); err != nil {
		t.Fatalf("CommitPrepared: %v", err)
	}
	if got := rowsOf(t, db, table); len(got) != len(ids) {
		t.Errorf("table has %d rows, want %d", len(got), len(ids))
	}
}

// ---------------------------------------------------------------------------
// Crash points, through the real coordinator.
//
// crashStore lets the coordinator's log write land and then "kills the
// process": every later log write fails and the context the round runs under
// is cancelled, so nothing after that point reaches the servers. The sinks are
// then closed, and a new coordinator over the same log, with new sinks, runs
// Recover — exactly what a restart does.
// ---------------------------------------------------------------------------

type crashStore struct {
	mu      sync.Mutex
	kv      map[string][]byte
	crashOn string // the record state whose durable write is the last thing to happen
	crashed bool
	fired   bool // the crash happens once; the restarted process writes the same states again
	cancel  context.CancelFunc
}

var errProcessDead = errors.New("the process is dead")

func (s *crashStore) Get(_ context.Context, k string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.kv[k]
	if !ok {
		return nil, twopc.ErrNotFound
	}
	return v, nil
}

func (s *crashStore) Set(_ context.Context, k string, v []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.crashed {
		return errProcessDead
	}
	s.kv[k] = v
	var rec struct {
		State string `json:"state"`
	}
	if !s.fired && json.Unmarshal(v, &rec) == nil && rec.State == s.crashOn {
		s.fired = true
		s.crashed = true
		s.cancel()
	}
	return nil
}

func (s *crashStore) Delete(_ context.Context, k string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.crashed {
		return errProcessDead
	}
	delete(s.kv, k)
	return nil
}

func (s *crashStore) List(_ context.Context, prefix string) (map[string][]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string][]byte{}
	for k, v := range s.kv {
		if strings.HasPrefix(k, prefix) {
			out[k] = v
		}
	}
	return out, nil
}

func (s *crashStore) restart() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.crashed = false
}

// crashThenRecover runs one round over two MySQL sinks that crashes right after
// the log records crashOn, checks both transactions are in doubt, then
// recovers with fresh sinks. It returns the two tables.
func crashThenRecover(t *testing.T, crashOn string) (*sql.DB, string, string) {
	t.Helper()
	dsn, db := xaDB(t)
	tableA := freshTable(t, db, "xa_crash_a")
	tableB := freshTable(t, db, "xa_crash_b")
	rollbackLeftovers(t, db)

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := &crashStore{kv: map[string][]byte{}, crashOn: crashOn, cancel: cancel}

	c, err := twopc.New(twopc.Options{Store: store, WorkflowID: "it-xa-crash"})
	if err != nil {
		t.Fatalf("twopc.New: %v", err)
	}
	a, b := sinkFor(dsn, tableA), sinkFor(dsn, tableB)
	err = c.Run(runCtx, []twopc.Participant{{ID: "a", Sink: a}, {ID: "b", Sink: b}},
		func(ctx context.Context) error {
			if err := a.WriteBatch(ctx, insertMsgs(t, []int64{1, 2}, "a")); err != nil {
				return err
			}
			return b.WriteBatch(ctx, insertMsgs(t, []int64{1, 2}, "b"))
		})
	if err == nil {
		t.Fatal("a round that crashed reported success")
	}
	// The process dies: its connections go with it.
	_ = a.Close()
	_ = b.Close()

	if n := len(recoveredXIDs(t, db)); n != 2 {
		t.Fatalf("after the crash XA RECOVER lists %d Hermod transactions, want 2 in doubt", n)
	}
	if got := rowsOf(t, db, tableA); len(got) != 0 {
		t.Fatalf("rows of an in-doubt transaction are visible: %v", got)
	}

	// Restart: a new coordinator over the same log, new sinks.
	store.restart()
	c2, err := twopc.New(twopc.Options{Store: store, WorkflowID: "it-xa-crash"})
	if err != nil {
		t.Fatalf("twopc.New after restart: %v", err)
	}
	a2, b2 := sinkFor(dsn, tableA), sinkFor(dsn, tableB)
	t.Cleanup(func() { _ = a2.Close(); _ = b2.Close() })
	if err := c2.Recover(context.Background(), map[string]hermod.TwoPhaseCommit{"a": a2, "b": b2}); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	if left := recoveredXIDs(t, db); len(left) != 0 {
		t.Errorf("after recovery XA RECOVER still lists %v; they are holding locks", left)
	}
	if n, _ := c2.InDoubt(context.Background()); n != 0 {
		t.Errorf("after recovery the log still has %d transaction(s)", n)
	}
	return db, tableA, tableB
}

// The commit decision was durable before the crash, so recovery commits.
func TestXACrashAfterTheCommitDecisionIsCommittedByRecovery(t *testing.T) {
	db, tableA, tableB := crashThenRecover(t, "committing")
	if got := rowsOf(t, db, tableA); len(got) != 2 {
		t.Errorf("table A has %v after recovery, want 2 rows: a decided commit was lost", got)
	}
	if got := rowsOf(t, db, tableB); len(got) != 2 {
		t.Errorf("table B has %v after recovery, want 2 rows: a decided commit was lost", got)
	}
}

// Both voted yes but no decision was recorded: presumed abort.
func TestXACrashAfterPrepareBeforeTheDecisionIsRolledBackByRecovery(t *testing.T) {
	db, tableA, tableB := crashThenRecover(t, "prepared")
	if got := rowsOf(t, db, tableA); len(got) != 0 {
		t.Errorf("table A has %v after recovery, want none: an undecided transaction was committed", got)
	}
	if got := rowsOf(t, db, tableB); len(got) != 0 {
		t.Errorf("table B has %v after recovery, want none: an undecided transaction was committed", got)
	}
}

// Before MySQL 8.0.27, with xa_detach_on_prepare=OFF, and on MariaDB, a
// prepared XA transaction stays attached to the session that prepared it, and
// no other connection can commit it while that session lives. Prepare discards
// its connection so this cannot matter; this runs the sink with detaching
// switched off to prove it. Returning the connection to the pool instead fails
// here, while passing on a default 8.4 server.
func TestXAPreparedTransactionIsResolvableWhenTheServerKeepsItAttached(t *testing.T) {
	dsn, db := xaDB(t)
	var detach string
	if err := db.QueryRowContext(context.Background(), "SELECT @@xa_detach_on_prepare").Scan(&detach); err != nil {
		t.Skipf("server has no xa_detach_on_prepare (before MySQL 8.0.27, or MariaDB): %v", err)
	}
	cfg, err := gomysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	if cfg.Params == nil {
		cfg.Params = map[string]string{}
	}
	// The driver sets unknown DSN parameters as session system variables.
	cfg.Params["xa_detach_on_prepare"] = "OFF"
	attached := cfg.FormatDSN()

	table := freshTable(t, db, "xa_attached")
	rollbackLeftovers(t, db)
	ctx := context.Background()

	// The preparing sink stays open throughout, as it does in a running
	// group: closing it would end every session in its pool and detach the
	// transaction regardless of what Prepare did.
	preparer := sinkFor(attached, table)
	defer preparer.Close()
	txID := uuid.NewString()
	if err := preparer.BeginNamed(ctx, txID); err != nil {
		t.Fatalf("BeginNamed: %v", err)
	}
	if err := preparer.WriteBatch(ctx, insertMsgs(t, []int64{1, 2}, "xa")); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}
	if _, err := preparer.Prepare(ctx, txID); err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	fresh := sinkFor(attached, table)
	defer fresh.Close()
	commitCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := fresh.CommitPrepared(commitCtx, txID); err != nil {
		t.Fatalf("CommitPrepared from another connection, with xa_detach_on_prepare=OFF: %v", err)
	}
	if got := rowsOf(t, db, table); len(got) != 2 {
		t.Errorf("table has %v after commit, want 2 rows", got)
	}
}
