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
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// A source that is closed after acknowledging every change must not hand any
// of them out again when it is opened on the same slot.
//
// The acknowledged position reached the server only in the periodic standby
// status update, every standbyMessageTimeout (10s), and Close sent none. So a
// stop confirmed nothing acknowledged since the last update, and the next
// source replayed up to ten seconds of changes the sinks already had. The
// branching load test found it: a restart under load re-sent 875 of 3,000
// Postgres records, and those bound for a RabbitMQ sink were published twice.
//
// Run with:
//
//	HERMOD_INTEGRATION=1 \
//	POSTGRES_DSN='postgres://postgres:postgres@127.0.0.1:5432/hermod_it?sslmode=disable' \
//	go test -tags=integration -run TestCloseConfirmsTheAcknowledgedPosition ./pkg/comm/source/postgres/
func TestCloseConfirmsTheAcknowledgedPosition(t *testing.T) {
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

	const table, slot, pub = "close_confirms_t", "close_confirms_slot", "close_confirms_pub"
	drop := func() {
		_, _ = db.ExecContext(context.Background(),
			"SELECT pg_drop_replication_slot($1) WHERE EXISTS "+
				"(SELECT 1 FROM pg_replication_slots WHERE slot_name = $1)", slot)
		_, _ = db.ExecContext(context.Background(), "DROP PUBLICATION IF EXISTS "+pub)
		_, _ = db.ExecContext(context.Background(), "DROP TABLE IF EXISTS "+table)
	}
	drop()
	t.Cleanup(drop)
	mustExec(t, db, fmt.Sprintf(`CREATE TABLE %s (id INT PRIMARY KEY)`, table))

	open := func() *PostgresSource {
		return NewPostgresSource(dsn, slot, pub, []string{table}, true, "", time.Second)
	}
	read := func(src *PostgresSource) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()
		msg, err := src.Read(ctx)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		id := fmt.Sprint(msg.Data()["id"])
		if err := src.Ack(ctx, msg); err != nil {
			t.Fatalf("ack %s: %v", id, err)
		}
		return id
	}

	first := open()
	// The first Read creates the slot; a row inserted before it is not captured.
	go func() {
		time.Sleep(time.Second)
		for i := 1; i <= 5; i++ {
			if _, err := db.ExecContext(context.Background(), fmt.Sprintf("INSERT INTO %s (id) VALUES ($1)", table), i); err != nil {
				t.Errorf("insert %d: %v", i, err)
			}
		}
	}()
	for i := 1; i <= 5; i++ {
		if got := read(first); got != strconv.Itoa(i) {
			t.Fatalf("first source read id %s, want %d", got, i)
		}
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	second := open()
	t.Cleanup(func() { _ = second.Close() })
	go func() {
		time.Sleep(time.Second)
		if _, err := db.ExecContext(context.Background(), fmt.Sprintf("INSERT INTO %s (id) VALUES (6)", table)); err != nil {
			t.Errorf("insert 6: %v", err)
		}
	}()
	if got := read(second); got != "6" {
		t.Fatalf("after a clean close, the next source started with id %s again: "+
			"Close did not confirm the acknowledged position to the slot", got)
	}
}
