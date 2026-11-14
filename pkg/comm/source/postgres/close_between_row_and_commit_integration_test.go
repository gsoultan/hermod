// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

//go:build integration

package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// A source closed after acknowledging a row, but before it has read the record
// that commits that row, must still not hand the row out again.
//
// Postgres sends a transaction as its rows followed by a Commit record, and on
// the next start it replays every transaction whose commit lies beyond the
// confirmed position. The source delivers a row as soon as it arrives, so the
// pipeline can acknowledge it, and the engine can stop the source, while the
// Commit is still unread in the socket. The position confirmed on stop was
// then the row's, which is before the commit, and the next source on the slot
// was handed the same transaction a second time. In CI this failed
// TestCloseConfirmsTheAcknowledgedPosition about once in several hundred runs
// ("the next source started with id 5 again").
//
// The afterEmit hook holds the stream goroutine in exactly that gap, so the
// stop lands there every time rather than once in several hundred.
//
// Run with:
//
//	HERMOD_INTEGRATION=1 \
//	POSTGRES_DSN='postgres://postgres:postgres@127.0.0.1:5432/hermod_it?sslmode=disable' \
//	go test -tags=integration -run TestCloseBetweenARowAndItsCommit ./pkg/comm/source/postgres/
func TestCloseBetweenARowAndItsCommitDoesNotReplayTheRow(t *testing.T) {
	f := newMidTxnFixture(t, "close_mid_txn")
	const rows = 5

	first := f.open()
	f.holdStreamAfterEmit(first, rows)
	inserted := f.later(func(ctx context.Context) error {
		for i := 1; i <= rows; i++ {
			if _, err := f.db.ExecContext(ctx, "INSERT INTO "+f.table+" (id) VALUES ($1)", i); err != nil {
				return fmt.Errorf("insert %d: %w", i, err)
			}
		}
		return nil
	})
	for i := 1; i <= rows; i++ {
		if got := f.readAndAck(first); got != strconv.Itoa(i) {
			t.Fatalf("first source read id %s, want %d", got, i)
		}
	}
	if err := <-inserted; err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	second := f.open()
	t.Cleanup(func() { _ = second.Close() })
	inserted = f.later(func(ctx context.Context) error {
		_, err := f.db.ExecContext(ctx, "INSERT INTO "+f.table+" (id) VALUES ($1)", rows+1)
		return err
	})
	got := f.readAndAck(second)
	if err := <-inserted; err != nil {
		t.Fatal(err)
	}
	if got != strconv.Itoa(rows+1) {
		t.Fatalf("the source was closed after acknowledging id %d, and the next source "+
			"started with id %s: the stop confirmed a position before that row's commit", rows, got)
	}
}

// The other side of the same stop: when the transaction has a further row that
// was never handed over, reading on to its Commit and confirming that would
// discard the row for good. The whole transaction has to come back instead.
func TestCloseBetweenTwoRowsOfATransactionReplaysIt(t *testing.T) {
	f := newMidTxnFixture(t, "close_mid_txn_rows")

	first := f.open()
	f.holdStreamAfterEmit(first, 1)
	inserted := f.later(func(ctx context.Context) error {
		tx, err := f.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		for i := 1; i <= 2; i++ {
			if _, err := tx.ExecContext(ctx, "INSERT INTO "+f.table+" (id) VALUES ($1)", i); err != nil {
				return fmt.Errorf("insert %d: %w", i, err)
			}
		}
		return tx.Commit()
	})
	if got := f.readAndAck(first); got != "1" {
		t.Fatalf("first source read id %s, want 1", got)
	}
	if err := <-inserted; err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	second := f.open()
	t.Cleanup(func() { _ = second.Close() })
	for _, want := range []string{"1", "2"} {
		if got := f.readAndAck(second); got != want {
			t.Fatalf("the source was closed with id 2 of the transaction undelivered, and the "+
				"next source read id %s where %s was due: the transaction was not replayed whole", got, want)
		}
	}
}

// midTxnFixture is a table, publication and slot of its own, on a real server.
type midTxnFixture struct {
	t     *testing.T
	db    *sql.DB
	dsn   string
	table string
	slot  string
	pub   string
}

func newMidTxnFixture(t *testing.T, name string) *midTxnFixture {
	t.Helper()
	dsn := os.Getenv("POSTGRES_DSN")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || dsn == "" {
		if os.Getenv("GITHUB_ACTIONS") == "true" {
			t.Fatalf("HERMOD_INTEGRATION=%q POSTGRES_DSN=%q in CI, where PostgreSQL is "+
				"started for exactly this", os.Getenv("HERMOD_INTEGRATION"), dsn)
		}
		t.Skip("integration: set HERMOD_INTEGRATION=1 and POSTGRES_DSN to run")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if lvl := scalar(t, db, "SELECT current_setting('wal_level')"); lvl != "logical" {
		t.Skipf("wal_level is %q, CDC needs 'logical'", lvl)
	}
	f := &midTxnFixture{t: t, db: db, dsn: dsn, table: name + "_t", slot: name + "_slot", pub: name + "_pub"}
	drop := func() {
		_, _ = db.ExecContext(context.Background(),
			"SELECT pg_drop_replication_slot($1) WHERE EXISTS "+
				"(SELECT 1 FROM pg_replication_slots WHERE slot_name = $1)", f.slot)
		_, _ = db.ExecContext(context.Background(), "DROP PUBLICATION IF EXISTS "+f.pub)
		_, _ = db.ExecContext(context.Background(), "DROP TABLE IF EXISTS "+f.table)
	}
	drop()
	t.Cleanup(drop)
	mustExec(t, db, fmt.Sprintf(`CREATE TABLE %s (id INT PRIMARY KEY)`, f.table))
	return f
}

func (f *midTxnFixture) open() *PostgresSource {
	return NewPostgresSource(f.dsn, f.slot, f.pub, []string{f.table}, true, "", time.Second)
}

// readAndAck reads one change, acknowledges it and returns its id.
func (f *midTxnFixture) readAndAck(src *PostgresSource) string {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(f.t.Context(), 20*time.Second)
	defer cancel()
	msg, err := src.Read(ctx)
	if err != nil {
		f.t.Fatalf("read: %v", err)
	}
	id := fmt.Sprint(msg.Data()["id"])
	if err := src.Ack(ctx, msg); err != nil {
		f.t.Fatalf("ack %s: %v", id, err)
	}
	return id
}

// later runs write once the source has had time to create its slot: a row
// written before the slot exists is not captured. The channel yields write's
// result, so nothing reports from a goroutine that outlives the test.
func (f *midTxnFixture) later(write func(ctx context.Context) error) <-chan error {
	done := make(chan error, 1)
	go func() {
		time.Sleep(time.Second)
		done <- write(context.Background())
	}()
	return done
}

// holdStreamAfterEmit parks the stream goroutine once it has handed over its
// nth change, until the source is being closed. Whatever follows that change
// on the wire is then still unread when the stop arrives.
func (f *midTxnFixture) holdStreamAfterEmit(src *PostgresSource, n int32) {
	var emitted atomic.Int32
	src.afterEmit = func(ctx context.Context) {
		if emitted.Add(1) == n {
			<-ctx.Done()
		}
	}
}
