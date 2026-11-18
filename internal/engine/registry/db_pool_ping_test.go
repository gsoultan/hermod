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
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
)

// --- An Exec SQL step sat for eighteen minutes with nothing on the server ---
//
// GetOrOpenDBByID pings the cached pool before handing it out, and the ping
// ran on the registry's lifetime context: no deadline, and not the caller's
// either. A pooled connection whose peer had gone without a word -- a
// firewall or NAT dropping it, a failover -- made that ping wait on a read
// that never comes. The step never reached its statement, so Postgres showed
// no query and no lock, and no step timeout or workflow stop could reach the
// goroutine, because the context it waited on was not theirs.

// silentServer accepts TCP connections and never answers: a peer that has
// gone away without closing anything, as far as the client can tell.
func silentServer(t *testing.T) string {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var conns []net.Conn
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			_ = c.Close()
		}
	})
	return ln.Addr().String()
}

// registryWithSilentPool is a registry whose cached pool for source "pg"
// points at a server that never answers.
func registryWithSilentPool(t *testing.T) (*Registry, pooledDB) {
	t.Helper()
	connStr := "postgres://u:p@" + silentServer(t) + "/db?sslmode=disable"
	src := storage.Source{ID: "pg", Type: "postgres", Config: map[string]string{"connection_string": connStr}}

	r := NewRegistry(&mockStorage{})
	r.sourceCacheMu.Lock()
	r.sourceCache[src.ID] = src
	r.sourceCacheMu.Unlock()

	db, err := openSQLDB("pgx", connStr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	entry := pooledDB{sourceID: src.ID, db: db}
	r.dbPoolMu.Lock()
	r.dbPool[dbPoolKey(src)] = entry
	r.dbPoolMu.Unlock()
	return r, entry
}

func getOrOpenWithin(ctx context.Context, t *testing.T, r *Registry, limit time.Duration) error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		_, _, err := r.GetOrOpenDBByID(ctx, "pg")
		done <- err
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(limit):
		t.Fatalf("GetOrOpenDBByID still waiting after %s on a server that never answers", limit)
		return nil
	}
}

func TestGetOrOpenDBByID_ReturnsWhenTheCallersContextEnds(t *testing.T) {
	r, _ := registryWithSilentPool(t)

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()

	if err := getOrOpenWithin(ctx, t, r, 5*time.Second); err == nil {
		t.Fatal("got a pool from a server that never answered, and the caller's context had ended")
	}
}

func TestGetOrOpenDBByID_BoundsThePingAndKeepsThePool(t *testing.T) {
	old := poolPingTimeout
	poolPingTimeout = 200 * time.Millisecond
	t.Cleanup(func() { poolPingTimeout = old })

	r, entry := registryWithSilentPool(t)

	// The caller has no deadline of its own: only the ping's bound can end it.
	if err := getOrOpenWithin(t.Context(), t, r, 5*time.Second); err != nil {
		t.Fatalf("a ping that only ran out of time failed the caller: %v", err)
	}

	// A ping that ran out of time says the pool was busy or one connection
	// was dead, not that the pool is wrong. Closing it would fail every
	// statement other messages have in flight on it.
	r.dbPoolMu.RLock()
	defer r.dbPoolMu.RUnlock()
	got, ok := r.dbPool[dbPoolKey(r.sourceCache["pg"])]
	if !ok || got.db != entry.db {
		t.Fatal("the pool was replaced after a ping that only ran out of time")
	}
}

// A pool opened without connect_timeout gets one; one that names its own keeps it.
func TestPgPoolConnConfig_BoundsTheConnect(t *testing.T) {
	cfg, err := pgPoolConnConfig("postgres://u:p@127.0.0.1:5432/db?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ConnectTimeout != pgConnectTimeout {
		t.Errorf("ConnectTimeout = %s with none in the DSN, want %s", cfg.ConnectTimeout, pgConnectTimeout)
	}

	cfg, err = pgPoolConnConfig("postgres://u:p@127.0.0.1:5432/db?sslmode=disable&connect_timeout=3")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ConnectTimeout != 3*time.Second {
		t.Errorf("ConnectTimeout = %s, want the DSN's own 3s", cfg.ConnectTimeout)
	}
}
