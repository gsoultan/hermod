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

package factory_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/factory"
	_ "github.com/sijms/go-ora/v2"
)

// Oracle LogMiner CDC, reached the way the running system reaches it: a stored
// source config through factory.CreateSource, against a real Oracle with
// archive logging and supplemental logging on. Updates and deletes must
// arrive as such, which the incremental mode cannot do at all.
//
// Run with:
//
//	HERMOD_INTEGRATION=1 ORACLE_HOST=127.0.0.1 ORACLE_PORT=1521 \
//	ORACLE_USER=hermod ORACLE_PASSWORD=hermod ORACLE_SERVICE=FREEPDB1 \
//	go test -tags=integration -run LogMiner ./internal/factory/
//
// Not run in CI: Oracle Free wants ~2GB and a slow first boot. docs/oracle-cdc.md
// lists the grants and settings the test user needs.
func TestOracleLogMinerSourceFromStoredConfigCapturesUpdatesAndDeletes(t *testing.T) {
	if os.Getenv("HERMOD_INTEGRATION") != "1" || os.Getenv("ORACLE_HOST") == "" {
		t.Skip("integration: set HERMOD_INTEGRATION=1 and ORACLE_HOST to run (not run in CI)")
	}
	cfg := hermod.StringMap{
		"host":       os.Getenv("ORACLE_HOST"),
		"port":       os.Getenv("ORACLE_PORT"),
		"user":       os.Getenv("ORACLE_USER"),
		"password":   os.Getenv("ORACLE_PASSWORD"),
		"dbname":     os.Getenv("ORACLE_SERVICE"),
		"tables":     "HLM_REACH",
		"cdc_method": "logminer",
	}
	dsn := factory.BuildConnectionString(cfg, "oracle")
	db, err := sql.Open("oracle", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(t.Context(), q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec("BEGIN EXECUTE IMMEDIATE 'DROP TABLE HLM_REACH'; EXCEPTION WHEN OTHERS THEN NULL; END;")
	exec("CREATE TABLE HLM_REACH (ID NUMBER PRIMARY KEY, NAME VARCHAR2(64))")
	exec("ALTER TABLE HLM_REACH ADD SUPPLEMENTAL LOG DATA (ALL) COLUMNS")
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), "BEGIN EXECUTE IMMEDIATE 'DROP TABLE HLM_REACH'; EXCEPTION WHEN OTHERS THEN NULL; END;")
	})

	src, err := factory.CreateSource(factory.SourceConfig{ID: "ora-lm", Type: "oracle", Config: cfg})
	if err != nil {
		t.Fatalf("CreateSource: %v", err)
	}
	t.Cleanup(func() { _ = src.Close() })
	if rc, ok := src.(hermod.ReadyChecker); ok {
		if err := rc.IsReady(t.Context()); err != nil {
			t.Fatalf("IsReady: %v", err)
		}
	}

	// The first Read fixes the start position at the current SCN; changes
	// are made after the reader is running.
	readCtx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	got := make(chan hermod.Message, 16)
	errs := make(chan error, 1)
	go func() {
		for {
			m, err := src.Read(readCtx)
			if err != nil {
				errs <- err
				return
			}
			_ = src.Ack(readCtx, m)
			got <- m
		}
	}()
	time.Sleep(3 * time.Second)

	exec("INSERT INTO HLM_REACH (ID, NAME) VALUES (1, 'a')")
	exec("UPDATE HLM_REACH SET NAME = 'b' WHERE ID = 1")
	exec("DELETE FROM HLM_REACH WHERE ID = 1")

	want := []struct {
		op     hermod.Operation
		before string
		after  string
	}{
		{hermod.OpCreate, "", `{"ID":1,"NAME":"a"}`},
		{hermod.OpUpdate, `{"ID":1,"NAME":"a"}`, `{"ID":1,"NAME":"b"}`},
		{hermod.OpDelete, `{"ID":1,"NAME":"b"}`, ""},
	}
	for i, w := range want {
		select {
		case m := <-got:
			if m.Operation() != w.op || m.Table() != "HLM_REACH" {
				t.Fatalf("change %d: op=%v table=%q, want %v HLM_REACH", i, m.Operation(), m.Table(), w.op)
			}
			if !jsonEq(t, m.Before(), w.before) || !jsonEq(t, m.After(), w.after) {
				t.Fatalf("change %d (%v): before=%s after=%s, want before=%s after=%s",
					i, w.op, m.Before(), m.After(), w.before, w.after)
			}
		case err := <-errs:
			t.Fatalf("Read: %v", err)
		case <-readCtx.Done():
			t.Fatalf("change %d (%v) never arrived", i, w.op)
		}
	}
}

func jsonEq(t *testing.T, got []byte, want string) bool {
	t.Helper()
	if want == "" {
		return len(got) == 0
	}
	var a, b any
	if err := json.Unmarshal(got, &a); err != nil {
		return false
	}
	if err := json.Unmarshal([]byte(want), &b); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprint(a) == fmt.Sprint(b)
}
