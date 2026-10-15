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

package oracle

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
)

// LogMiner CDC against a real Oracle. The database needs ARCHIVELOG mode,
// minimal supplemental logging, and the grants in docs/oracle-cdc.md.
//
//	HERMOD_INTEGRATION=1 \
//	ORACLE_DSN='oracle://hermod:hermod@127.0.0.1:1521/FREEPDB1' \
//	ORACLE_ROOT_DSN='oracle://system:oracle@127.0.0.1:1521/FREE' \
//	go test -tags=integration -run LogMiner ./pkg/comm/source/oracle/
//
// ORACLE_ROOT_DSN is optional: it runs the CDB$ROOT case, where the logs are
// registered explicitly and the PDB is named in the source's config.

func makeLMTable(t *testing.T, db *sql.DB, name string) {
	t.Helper()
	drop := func() {
		_, _ = db.ExecContext(context.Background(),
			fmt.Sprintf("BEGIN EXECUTE IMMEDIATE 'DROP TABLE %s'; EXCEPTION WHEN OTHERS THEN NULL; END;", name))
	}
	drop()
	t.Cleanup(drop)
	for _, q := range []string{
		fmt.Sprintf("CREATE TABLE %s (ID NUMBER PRIMARY KEY, NAME VARCHAR2(64), AMOUNT NUMBER(10,2), CREATED DATE)", name),
		fmt.Sprintf("ALTER TABLE %s ADD SUPPLEMENTAL LOG DATA (ALL) COLUMNS", name),
	} {
		if _, err := db.ExecContext(t.Context(), q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

func lmExec(t *testing.T, db *sql.DB, qs ...string) {
	t.Helper()
	for _, q := range qs {
		if _, err := db.ExecContext(t.Context(), q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

// lmRead reads n messages, acknowledging each, or fails.
func lmRead(t *testing.T, src *OracleSource, n int) []hermod.Message {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	var out []hermod.Message
	for len(out) < n {
		m, err := src.Read(ctx)
		if err != nil {
			t.Fatalf("after %d of %d messages: %v", len(out), n, err)
		}
		if err := src.Ack(ctx, m); err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

// lmNothingMore asserts no further message arrives within a few windows.
func lmNothingMore(t *testing.T, src *OracleSource) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	if m, err := src.Read(ctx); err == nil {
		t.Fatalf("unexpected message %v %s %s", m.Operation(), m.Before(), m.After())
	}
}

func newLMSource(dsn, table, pdb string, initialLoad bool) *OracleSource {
	s := NewOracleSource(dsn, []string{table}, "", 500*time.Millisecond, true)
	s.SetLogMiner(pdb, initialLoad)
	return s
}

// primeLM makes the first Read happen, which fixes the start SCN, before the
// test writes anything.
func primeLM(t *testing.T, src *OracleSource) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if m, err := src.Read(ctx); err == nil {
		t.Fatalf("a fresh source with no initial load emitted %v", m.Operation())
	}
}

func ops(msgs []hermod.Message) string {
	var b []string
	for _, m := range msgs {
		b = append(b, fmt.Sprintf("%s %s>%s", m.Operation(), m.Before(), m.After()))
	}
	return strings.Join(b, " | ")
}

func requireLMOracle(t *testing.T) (string, *sql.DB) {
	t.Helper()
	dsn, db, _ := requireOracle(t)
	return dsn, db
}

func TestLogMinerTypesNullsAndRollbacks(t *testing.T) {
	dsn, db := requireLMOracle(t)
	makeLMTable(t, db, "HLM_TYPES")
	src := newLMSource(dsn, "HLM_TYPES", "", false)
	t.Cleanup(func() { _ = src.Close() })
	primeLM(t, src)

	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = tx.ExecContext(t.Context(), "INSERT INTO HLM_TYPES VALUES (9, 'never', 1, NULL)")
	_ = tx.Rollback()

	lmExec(t, db,
		"INSERT INTO HLM_TYPES VALUES (1, 'a', 12.5, TO_DATE('2026-10-11 07:30:18', 'YYYY-MM-DD HH24:MI:SS'))",
		"UPDATE HLM_TYPES SET NAME = NULL WHERE ID = 1",
	)
	got := lmRead(t, src, 2)
	if got[0].Operation() != hermod.OpCreate ||
		string(got[0].After()) != `{"AMOUNT":12.5,"CREATED":"2026-10-11T07:30:18","ID":1,"NAME":"a"}` {
		t.Fatalf("insert: %s", ops(got[:1]))
	}
	if got[1].Operation() != hermod.OpUpdate ||
		string(got[1].After()) != `{"AMOUNT":12.5,"CREATED":"2026-10-11T07:30:18","ID":1,"NAME":null}` {
		t.Fatalf("update to NULL: %s", ops(got[1:]))
	}
	lmNothingMore(t, src)
}

func TestLogMinerTransactionSpanningWindowsAndRestart(t *testing.T) {
	dsn, db := requireLMOracle(t)
	makeLMTable(t, db, "HLM_RESUME")
	src := newLMSource(dsn, "HLM_RESUME", "", false)
	primeLM(t, src)

	// A transaction left open across several mining windows, with another
	// committing inside it.
	long, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := long.ExecContext(t.Context(), "INSERT INTO HLM_RESUME (ID, NAME) VALUES (1, 'long')"); err != nil {
		t.Fatal(err)
	}
	lmExec(t, db, "INSERT INTO HLM_RESUME (ID, NAME) VALUES (2, 'short')")
	got := lmRead(t, src, 1)
	if !strings.Contains(string(got[0].After()), `"short"`) {
		t.Fatalf("want the short transaction first: %s", ops(got))
	}
	state := src.GetState()
	_ = src.Close()

	// The long transaction commits while the source is down. A restart from
	// the saved state must deliver it, and must not deliver the short one
	// again.
	if _, err := long.ExecContext(t.Context(), "UPDATE HLM_RESUME SET NAME = 'long2' WHERE ID = 1"); err != nil {
		t.Fatal(err)
	}
	if err := long.Commit(); err != nil {
		t.Fatal(err)
	}
	lmExec(t, db, "DELETE FROM HLM_RESUME WHERE ID = 2")

	again := newLMSource(dsn, "HLM_RESUME", "", false)
	t.Cleanup(func() { _ = again.Close() })
	again.SetState(state)
	got = lmRead(t, again, 3)
	if got[0].Operation() != hermod.OpCreate || !strings.Contains(string(got[0].After()), `"long"`) ||
		got[1].Operation() != hermod.OpUpdate || !strings.Contains(string(got[1].After()), `"long2"`) ||
		got[2].Operation() != hermod.OpDelete || !strings.Contains(string(got[2].Before()), `"short"`) {
		t.Fatalf("after restart: %s", ops(got))
	}
	lmNothingMore(t, again)
}

func TestLogMinerInitialLoadThenChanges(t *testing.T) {
	dsn, db := requireLMOracle(t)
	makeLMTable(t, db, "HLM_LOAD")
	lmExec(t, db, "INSERT INTO HLM_LOAD (ID, NAME) VALUES (1, 'existing')")
	src := newLMSource(dsn, "HLM_LOAD", "", true)
	t.Cleanup(func() { _ = src.Close() })

	got := lmRead(t, src, 1)
	if got[0].Operation() != hermod.OpSnapshot || !strings.Contains(string(got[0].After()), `"existing"`) {
		t.Fatalf("initial load: %s", ops(got))
	}
	if src.GetState() == nil {
		t.Fatal("once the loaded rows are acknowledged the position must be reported")
	}
	lmExec(t, db, "UPDATE HLM_LOAD SET NAME = 'changed' WHERE ID = 1")
	got = lmRead(t, src, 1)
	if got[0].Operation() != hermod.OpUpdate || !strings.Contains(string(got[0].After()), `"changed"`) {
		t.Fatalf("change after load: %s", ops(got))
	}
}

func TestLogMinerIsReadyNamesMissingSupplementalLogging(t *testing.T) {
	dsn, db := requireLMOracle(t)
	lmExec(t, db, "BEGIN EXECUTE IMMEDIATE 'DROP TABLE HLM_NOSUPP'; EXCEPTION WHEN OTHERS THEN NULL; END;",
		"CREATE TABLE HLM_NOSUPP (ID NUMBER PRIMARY KEY)")
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), "BEGIN EXECUTE IMMEDIATE 'DROP TABLE HLM_NOSUPP'; EXCEPTION WHEN OTHERS THEN NULL; END;")
	})
	src := newLMSource(dsn, "HLM_NOSUPP", "", false)
	t.Cleanup(func() { _ = src.Close() })
	err := src.IsReady(t.Context())
	if err == nil || !strings.Contains(err.Error(), "ADD SUPPLEMENTAL LOG DATA (ALL) COLUMNS") {
		t.Fatalf("IsReady = %v, want the statement that fixes it", err)
	}
}

func TestLogMinerFromCDBRootWithExplicitLogs(t *testing.T) {
	rootDSN := os.Getenv("ORACLE_ROOT_DSN")
	if rootDSN == "" {
		t.Skip("set ORACLE_ROOT_DSN to run the CDB$ROOT case")
	}
	_, db := requireLMOracle(t)
	makeLMTable(t, db, "HLM_ROOT")
	var owner string
	if err := db.QueryRowContext(t.Context(), "SELECT USER FROM DUAL").Scan(&owner); err != nil {
		t.Fatal(err)
	}
	src := newLMSource(rootDSN, owner+".HLM_ROOT", "FREEPDB1", false)
	t.Cleanup(func() { _ = src.Close() })
	primeLM(t, src)
	lmExec(t, db, "INSERT INTO HLM_ROOT (ID, NAME) VALUES (1, 'r')", "DELETE FROM HLM_ROOT WHERE ID = 1")
	got := lmRead(t, src, 2)
	if got[0].Operation() != hermod.OpCreate || got[1].Operation() != hermod.OpDelete {
		t.Fatalf("from root: %s", ops(got))
	}
	if lm := src.logMinerReader(); lm == nil || !lm.explicitLogs {
		t.Fatal("mining from CDB$ROOT must have registered the log files itself")
	}
}
