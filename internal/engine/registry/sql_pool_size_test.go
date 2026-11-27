// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package registry

import (
	"database/sql"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
)

// --- DB Lookup timed out with 1000 workflows on one database ---
//
// The pool the registry hands DB Lookup and Execute SQL was capped at 20
// connections, hard-coded, whatever the source said. PR 436 put a Max
// connections field on the source form, which writes pool_max_conns into the
// DSN, but only pgxpool reads that key. This pool parses with pgx.ParseConfig,
// which forwards any key it does not know to the server as a startup
// parameter: PostgreSQL refuses the connection ("unrecognized configuration
// parameter") and so does PgBouncer ("unsupported startup parameter"). So
// raising the limit on the source broke its lookups outright, and leaving it
// alone kept every lookup on the source queued behind 20 connections.

func TestPgPoolConnConfig_TakesThePoolSizeAndDoesNotSendItToTheServer(t *testing.T) {
	cfg, maxConns, err := pgPoolConnConfig("postgres://u:p@127.0.0.1:5432/db?sslmode=disable&pool_max_conns=120&pool_min_conns=2")
	if err != nil {
		t.Fatal(err)
	}
	if maxConns != 120 {
		t.Errorf("maxConns = %d, want the DSN's 120", maxConns)
	}
	for k := range cfg.RuntimeParams {
		if len(k) >= 5 && k[:5] == "pool_" {
			t.Errorf("startup parameter %q would be sent to the server, which refuses it", k)
		}
	}
}

func TestPgPoolConnConfig_DefaultsThePoolSize(t *testing.T) {
	_, maxConns, err := pgPoolConnConfig("postgres://u:p@127.0.0.1:5432/db?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	if maxConns != defaultSQLPoolMaxConns {
		t.Errorf("maxConns = %d with none in the DSN, want %d", maxConns, defaultSQLPoolMaxConns)
	}
}

func TestPgPoolConnConfig_RejectsANonPositivePoolSize(t *testing.T) {
	for _, v := range []string{"0", "-3", "many"} {
		if _, _, err := pgPoolConnConfig("postgres://u:p@127.0.0.1:5432/db?pool_max_conns=" + v); err == nil {
			t.Errorf("pool_max_conns=%s accepted; a typo must not silently become a different limit", v)
		}
	}
}

func TestSizeSQLPool_AppliesTheLimit(t *testing.T) {
	db, err := openSQLDB("pgx", "postgres://u:p@127.0.0.1:1/db?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	sizeSQLPool(db, 120)
	if got := db.Stats().MaxOpenConnections; got != 120 {
		t.Errorf("MaxOpenConnections = %d, want 120", got)
	}
}

// --- Every lookup pinged before it queried ---
//
// GetOrOpenDB pinged the cached pool on every call, and DB Lookup calls it for
// every message. Each lookup therefore took a connection twice and made two
// round trips -- through PgBouncer in transaction mode, two server
// assignments -- halving what the pool and the pooler could serve. When the
// pool was saturated the ping itself queued for a connection for up to 5s
// before the query even started. A pool that answered a moment ago is not
// asked again until poolCheckInterval has passed.

func TestCachedPool_DoesNotPingAPoolThatWasJustChecked(t *testing.T) {
	connStr := "postgres://u:p@" + silentServer(t) + "/db?sslmode=disable"
	src := storage.Source{ID: "pg", Type: "postgres", Config: map[string]string{"connection_string": connStr}}
	r := NewRegistry(&mockStorage{})
	db, err := openSQLDB("pgx", connStr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	entry := newPooledDB(src.ID, db)
	r.dbPoolMu.Lock()
	r.dbPool[dbPoolKey(src)] = entry
	r.dbPoolMu.Unlock()

	// A ping on this pool would hang on the silent server until the caller's
	// deadline; one that is skipped returns at once.
	start := time.Now()
	got, err := r.GetOrOpenDB(src)
	if err != nil {
		t.Fatal(err)
	}
	if got != db {
		t.Fatal("a different pool was returned")
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("GetOrOpenDB took %s: it pinged a pool that was checked a moment ago", d)
	}
}

func TestPooledDB_PingsAgainOnceTheCheckIsOld(t *testing.T) {
	old := poolCheckInterval
	poolCheckInterval = time.Millisecond
	t.Cleanup(func() { poolCheckInterval = old })

	e := newPooledDB("pg", &sql.DB{})
	time.Sleep(5 * time.Millisecond)
	if e.recentlyChecked() {
		t.Error("a check older than poolCheckInterval still counted as recent")
	}
	e.markChecked()
	poolCheckInterval = time.Hour
	if !e.recentlyChecked() {
		t.Error("a check just made did not count as recent")
	}
	// An entry built without the clock (tests, older code) always pings.
	if (pooledDB{}).recentlyChecked() {
		t.Error("an entry with no check clock skipped its ping")
	}
}
