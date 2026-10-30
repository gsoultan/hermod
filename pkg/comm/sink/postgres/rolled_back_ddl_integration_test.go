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
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/infra/sqlutil"
	"github.com/jackc/pgx/v5/pgxpool"
)

// A table the sink creates inside a transaction disappears when that
// transaction rolls back — PostgreSQL DDL is transactional. The sink caches
// "this table exists" so it checks once, not once per message, and that cache
// used to be filled before the transaction had committed. After a rollback it
// went on believing in a table that was never created, and every later write
// failed with "relation does not exist" until the process restarted.
//
// In a transactional group that is the first round of a new workflow: any
// member failing (a MySQL member refusing its own CREATE TABLE was how this was
// found, running the app end to end) rolls back the PostgreSQL member's CREATE
// TABLE too, and the group then retries forever against a table that is not
// there.
//
// Run with:
//
//	HERMOD_INTEGRATION=1 \
//	POSTGRES_DSN='postgres://postgres:postgres@127.0.0.1:5432/hermod_it?sslmode=disable' \
//	go test -tags=integration -run RolledBack ./pkg/comm/sink/postgres/

func rolledBackDDLFixture(t *testing.T) (*pgxpool.Pool, string, *PostgresSink) {
	t.Helper()
	dsn := preparedDSN(t)
	pool, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatalf("pgxpool: %v", err)
	}
	t.Cleanup(pool.Close)

	table := "hermod_rolled_back_ddl_" + uuid.NewString()[:8]
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), fmt.Sprintf("DROP TABLE IF EXISTS %s", table))
	})

	// An INTEGER key, so a message can be made to fail at INSERT — after the
	// sink has already created the table in the same transaction.
	mappings := []sqlutil.ColumnMapping{
		{SourceField: "id", TargetColumn: "id", DataType: "INTEGER", IsPrimaryKey: true},
		{SourceField: "val", TargetColumn: "val"},
	}
	sink := NewPostgresSink(dsn, table, mappings, false, "", "", "", "", false, false)
	t.Cleanup(func() { _ = sink.Close() })
	return pool, table, sink
}

func rolledBackDDLMessage(t *testing.T, id string) hermod.Message {
	t.Helper()
	m := message.AcquireMessage()
	t.Cleanup(m.Release)
	m.SetID(id)
	m.SetOperation(hermod.OpCreate)
	m.SetData("id", id)
	m.SetData("val", "v"+id)
	return m
}

func rolledBackDDLRows(t *testing.T, pool *pgxpool.Pool, table string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), fmt.Sprintf("SELECT count(*) FROM %s", table)).Scan(&n); err != nil {
		t.Fatalf("counting rows in %s: %v", table, err)
	}
	return n
}

// The coordinator abandons a round (another member failed) and rolls this one
// back before it prepared.
func TestRolledBackRoundDoesNotLeaveATableTheNextRoundCannotFind(t *testing.T) {
	pool, table, sink := rolledBackDDLFixture(t)

	if err := sink.Begin(t.Context()); err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := sink.WriteBatch(t.Context(), []hermod.Message{rolledBackDDLMessage(t, "1")}); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := sink.Rollback(t.Context()); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	if err := sink.Begin(t.Context()); err != nil {
		t.Fatalf("begin again: %v", err)
	}
	if err := sink.WriteBatch(t.Context(), []hermod.Message{rolledBackDDLMessage(t, "2")}); err != nil {
		_ = sink.Rollback(t.Context())
		t.Fatalf("the round after a rolled-back one failed: %v\n"+
			"the rollback took the CREATE TABLE with it, and the sink still believed the table existed", err)
	}
	if err := sink.Commit(t.Context()); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if got := rolledBackDDLRows(t, pool, table); got != 1 {
		t.Errorf("%s has %d rows, want 1", table, got)
	}
}

// The round was prepared, and the coordinator then decided to abort it.
func TestRolledBackPreparedRoundDoesNotLeaveATableTheNextRoundCannotFind(t *testing.T) {
	pool, table, sink := rolledBackDDLFixture(t)

	if err := sink.Begin(t.Context()); err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := sink.WriteBatch(t.Context(), []hermod.Message{rolledBackDDLMessage(t, "1")}); err != nil {
		t.Fatalf("first write: %v", err)
	}
	txID, err := sink.Prepare(t.Context(), uuid.NewString())
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if err := sink.RollbackPrepared(t.Context(), txID); err != nil {
		t.Fatalf("rollback prepared: %v", err)
	}

	if err := sink.Begin(t.Context()); err != nil {
		t.Fatalf("begin again: %v", err)
	}
	if err := sink.WriteBatch(t.Context(), []hermod.Message{rolledBackDDLMessage(t, "2")}); err != nil {
		_ = sink.Rollback(t.Context())
		t.Fatalf("the round after a rolled-back prepared one failed: %v", err)
	}
	next, err := sink.Prepare(t.Context(), uuid.NewString())
	if err != nil {
		t.Fatalf("prepare again: %v", err)
	}
	if err := sink.CommitPrepared(t.Context(), next); err != nil {
		t.Fatalf("commit prepared: %v", err)
	}
	if got := rolledBackDDLRows(t, pool, table); got != 1 {
		t.Errorf("%s has %d rows, want 1", table, got)
	}
}

// Outside a group the sink runs its own transaction, and a batch that fails
// part-way rolls back the table it created at the start.
func TestFailedBatchDoesNotLeaveATableTheNextBatchCannotFind(t *testing.T) {
	pool, table, sink := rolledBackDDLFixture(t)

	// "x" is not an INTEGER, so the batch fails after CREATE TABLE ran.
	if err := sink.WriteBatch(t.Context(), []hermod.Message{
		rolledBackDDLMessage(t, "1"),
		rolledBackDDLMessage(t, "x"),
	}); err == nil {
		t.Fatal("a batch with a non-integer key was accepted; the fixture no longer fails where it must")
	}

	if err := sink.WriteBatch(t.Context(), []hermod.Message{rolledBackDDLMessage(t, "2")}); err != nil {
		t.Fatalf("the batch after a failed one failed: %v", err)
	}
	if got := rolledBackDDLRows(t, pool, table); got != 1 {
		t.Errorf("%s has %d rows, want 1", table, got)
	}
}

// A committed round keeps the cache, so the existence check is not repeated.
// Guards against fixing the above by never caching at all.
func TestCommittedRoundKeepsTheTableVerified(t *testing.T) {
	_, table, sink := rolledBackDDLFixture(t)

	if err := sink.Begin(t.Context()); err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := sink.WriteBatch(t.Context(), []hermod.Message{rolledBackDDLMessage(t, "1")}); err != nil {
		t.Fatalf("write: %v", err)
	}
	txID, err := sink.Prepare(t.Context(), uuid.NewString())
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if err := sink.CommitPrepared(t.Context(), txID); err != nil {
		t.Fatalf("commit prepared: %v", err)
	}
	if _, ok := sink.verifiedTables.Load(table); !ok {
		t.Errorf("%s is not cached as verified after its round committed", table)
	}
}
