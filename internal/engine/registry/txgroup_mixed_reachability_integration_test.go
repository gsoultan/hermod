// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

//go:build integration

package registry

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/factory"
	"github.com/gsoultan/hermod/internal/storage"
)

// ---------------------------------------------------------------------------
// Reachability: a group spanning PostgreSQL and MySQL, built the way a
// workflow builds one.
//
// The MySQL sink takes part through XA transactions. Like the PostgreSQL-only
// tests in txgroup_reachability_integration_test.go, these start from stored
// sink records and go through the registry and the factory, so they fail if
// MySQL is unreachable as a member — not listed as two-phase capable, wrapped
// in decorators, or missing the interface the coordinator uses to name an XA
// transaction at XA START.
//
//	HERMOD_INTEGRATION=1 POSTGRES_DSN='postgres://...' \
//	MYSQL_DSN='root:root@tcp(127.0.0.1:3307)/hermod_test' \
//	go test -tags=integration -run Mixed ./internal/engine/registry/
// ---------------------------------------------------------------------------

func mixedMySQL(t *testing.T) (string, *sql.DB) {
	t.Helper()
	dsn := os.Getenv("MYSQL_DSN")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || dsn == "" {
		t.Skip("integration: set HERMOD_INTEGRATION=1, POSTGRES_DSN and MYSQL_DSN to run")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open mysql: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("MYSQL_DSN is not reachable: %v", err)
	}
	return dsn, db
}

// makeMySQLTable creates a table for one test. check, if set, is a CHECK
// constraint the write must violate.
func makeMySQLTable(t *testing.T, db *sql.DB, name, check string) string {
	t.Helper()
	table := fmt.Sprintf("reach_%s_%d", name, os.Getpid())
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, "DROP TABLE IF EXISTS "+table); err != nil {
		t.Fatalf("drop %s: %v", table, err)
	}
	ddl := fmt.Sprintf("CREATE TABLE %s (id VARCHAR(64) PRIMARY KEY, val TEXT)", table)
	if check != "" {
		ddl = fmt.Sprintf("CREATE TABLE %s (id VARCHAR(64) PRIMARY KEY, val VARCHAR(64), CHECK (%s))", table, check)
	}
	if _, err := db.ExecContext(ctx, ddl); err != nil {
		t.Fatalf("create %s: %v", table, err)
	}
	t.Cleanup(func() { _, _ = db.ExecContext(context.Background(), "DROP TABLE IF EXISTS "+table) })
	// Registered after the DROP so it runs first: a prepared XA transaction
	// left behind by a failing test holds a metadata lock the DROP would wait
	// on forever.
	t.Cleanup(func() {
		for _, xid := range mysqlInDoubt(t, db) {
			_, _ = db.ExecContext(context.Background(), "XA ROLLBACK "+xid)
		}
	})
	return table
}

func mysqlRowCount(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// hermodXAFormatID is the MySQL sink's XID format ID ("HMOD"). Restated rather
// than imported, so this test reads the server the way an operator would.
const hermodXAFormatID = 0x484D4F44

// mysqlInDoubt lists Hermod's prepared XA transactions from XA RECOVER, as XID
// clauses ready for XA ROLLBACK.
func mysqlInDoubt(t *testing.T, db *sql.DB) []string {
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
		if formatID == hermodXAFormatID {
			out = append(out, fmt.Sprintf("'%s','',%d", data[:gtridLen], formatID))
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("XA RECOVER rows: %v", err)
	}
	return out
}

// storedMySQLSink is a MySQL sink record of the kind an operator creates.
func storedMySQLSink(id, dsn, table string) storage.Sink {
	return storage.Sink{
		ID:   id,
		Name: id,
		Type: "mysql",
		Config: map[string]string{
			"connection_string":  dsn,
			"table":              table,
			"use_existing_table": "true",
			"column_mappings": `[{"source_field":"id","target_column":"id","is_primary_key":true},` +
				`{"source_field":"val","target_column":"val"}]`,
		},
	}
}

// buildStoredGroup builds a txgroup exactly as the engine does for a node of
// that type.
func buildStoredGroup(t *testing.T, sinks map[string]storage.Sink, id, members string) hermod.BatchSink {
	t.Helper()
	r := registryWithSinks(sinks)
	group, err := r.createSinkInternal(context.Background(), factory.SinkConfig{
		ID:     id,
		Type:   "txgroup",
		Config: map[string]string{"members": members},
	})
	if err != nil {
		t.Fatalf("building a PostgreSQL + MySQL group from stored configuration: %v", err)
	}
	t.Cleanup(func() { _ = group.Close() })
	batch, ok := group.(hermod.BatchSink)
	if !ok {
		t.Fatalf("the group built from configuration is %T, which is not a hermod.BatchSink", group)
	}
	return batch
}

func TestAMixedPostgresAndMySQLGroupBuiltFromStoredConfigurationWrites(t *testing.T) {
	pgDSN := reachabilityDSN(t)
	p := reachabilityPool(t, pgDSN)
	requirePrepared(t, p)
	myDSN, my := mixedMySQL(t)

	pgTable := makeReachabilityTable(t, p, "mixed_pg")
	myTable := makeMySQLTable(t, my, "mixed_my", "")

	group := buildStoredGroup(t, map[string]storage.Sink{
		"sink-pg": storedPostgresSink("sink-pg", pgDSN, pgTable),
		"sink-my": storedMySQLSink("sink-my", myDSN, myTable),
	}, "grp-mixed", "sink-pg,sink-my")

	pgBefore := reachabilityPreparedCount(t, p)

	if err := group.WriteBatch(context.Background(), []hermod.Message{
		reachabilityMessage("1", "one"),
		reachabilityMessage("2", "two"),
	}); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}

	if got := reachabilityRowCount(t, p, pgTable); got != 2 {
		t.Errorf("PostgreSQL member has %d rows, want 2", got)
	}
	if got := mysqlRowCount(t, my, myTable); got != 2 {
		t.Errorf("MySQL member has %d rows, want 2", got)
	}
	if got := reachabilityPreparedCount(t, p); got != pgBefore {
		t.Errorf("pg_prepared_xacts went from %d to %d; a transaction is still holding locks", pgBefore, got)
	}
	if left := mysqlInDoubt(t, my); len(left) != 0 {
		t.Errorf("XA RECOVER still lists %v after a clean commit; they are holding locks", left)
	}
}

// Atomic in both directions: whichever database refuses the write, the other
// keeps nothing and neither is left in doubt.
func TestAMixedGroupFromStoredConfigurationIsAtomic(t *testing.T) {
	pgDSN := reachabilityDSN(t)
	p := reachabilityPool(t, pgDSN)
	requirePrepared(t, p)
	myDSN, my := mixedMySQL(t)

	t.Run("MySQL refuses after PostgreSQL wrote", func(t *testing.T) {
		pgTable := makeReachabilityTable(t, p, "mixed_atomic_pg")
		myTable := makeMySQLTable(t, my, "mixed_atomic_my_bad", "val = 'nothing writes this'")

		group := buildStoredGroup(t, map[string]storage.Sink{
			"sink-pg": storedPostgresSink("sink-pg", pgDSN, pgTable),
			"sink-my": storedMySQLSink("sink-my", myDSN, myTable),
		}, "grp-mixed-atomic-1", "sink-pg,sink-my")

		pgBefore := reachabilityPreparedCount(t, p)
		if err := group.WriteBatch(context.Background(), []hermod.Message{
			reachabilityMessage("1", "one"),
		}); err == nil {
			t.Fatal("a write MySQL refused reported success")
		}
		if got := reachabilityRowCount(t, p, pgTable); got != 0 {
			t.Errorf("PostgreSQL kept %d rows after the group failed; the write was not atomic", got)
		}
		if got := reachabilityPreparedCount(t, p); got != pgBefore {
			t.Errorf("pg_prepared_xacts went from %d to %d; a failed write left a transaction in doubt", pgBefore, got)
		}
		if left := mysqlInDoubt(t, my); len(left) != 0 {
			t.Errorf("XA RECOVER lists %v after a failed write", left)
		}
	})

	t.Run("PostgreSQL refuses after MySQL wrote", func(t *testing.T) {
		myTable := makeMySQLTable(t, my, "mixed_atomic_my", "")
		pgTable := fmt.Sprintf("reach_mixed_atomic_pg_bad_%d", os.Getpid())
		ctx := context.Background()
		if _, err := p.Exec(ctx, "DROP TABLE IF EXISTS "+pgTable); err != nil {
			t.Fatalf("drop %s: %v", pgTable, err)
		}
		if _, err := p.Exec(ctx, fmt.Sprintf(
			`CREATE TABLE %s (id TEXT PRIMARY KEY, val TEXT CHECK (val = 'nothing writes this'))`, pgTable)); err != nil {
			t.Fatalf("create %s: %v", pgTable, err)
		}
		t.Cleanup(func() { _, _ = p.Exec(context.Background(), "DROP TABLE IF EXISTS "+pgTable) })

		group := buildStoredGroup(t, map[string]storage.Sink{
			"sink-my": storedMySQLSink("sink-my", myDSN, myTable),
			"sink-pg": storedPostgresSink("sink-pg", pgDSN, pgTable),
		}, "grp-mixed-atomic-2", "sink-my,sink-pg")

		pgBefore := reachabilityPreparedCount(t, p)
		if err := group.WriteBatch(ctx, []hermod.Message{
			reachabilityMessage("1", "one"),
		}); err == nil {
			t.Fatal("a write PostgreSQL refused reported success")
		}
		if got := mysqlRowCount(t, my, myTable); got != 0 {
			t.Errorf("MySQL kept %d rows after the group failed; the write was not atomic", got)
		}
		if left := mysqlInDoubt(t, my); len(left) != 0 {
			t.Errorf("XA RECOVER lists %v after a failed write", left)
		}
		if got := reachabilityPreparedCount(t, p); got != pgBefore {
			t.Errorf("pg_prepared_xacts went from %d to %d after a failed write", pgBefore, got)
		}
	})
}
