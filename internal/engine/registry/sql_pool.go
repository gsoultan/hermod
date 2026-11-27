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
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gsoultan/hermod/pkg/infra/pgxutil"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// defaultSQLPoolMaxConns is the size of the database/sql pool a source gets
// for DB Lookup, Execute SQL and the other per-message statements, when its
// connection string sets no pool_max_conns (the source form's Max
// connections). It is per source, per worker process.
const defaultSQLPoolMaxConns = 20

// sqlPoolMaxIdleConns caps the idle connections a pool keeps between bursts.
const sqlPoolMaxIdleConns = 10

// poolCheckInterval is how long a pool that answered a ping, or was too busy to
// answer one, is handed out without being pinged again. A variable so tests
// can shorten it.
//
// GetOrOpenDB used to ping on every call, and DB Lookup calls it for every
// message: each lookup took a connection twice and made two round trips, and
// on a saturated pool the ping itself queued for a connection for up to
// poolPingTimeout before the query could start. database/sql already discards
// a connection the driver reports as bad, so the ping is only there to notice
// a pool that has to be opened again, and that does not need checking more
// than a few times a minute.
var poolCheckInterval = 30 * time.Second

// pooledDB is a cached connection pool together with the source it was opened
// for, so a source's older pools can be identified and closed when its
// connection details change.
type pooledDB struct {
	sourceID string
	db       *sql.DB
	// checked is when the pool last answered a ping or was busy, in Unix
	// nanoseconds. Nil means always ping.
	checked *atomic.Int64
}

// newPooledDB is a cache entry for a pool that has just been opened and so
// counts as checked.
func newPooledDB(sourceID string, db *sql.DB) pooledDB {
	e := pooledDB{sourceID: sourceID, db: db, checked: new(atomic.Int64)}
	e.markChecked()
	return e
}

func (e pooledDB) recentlyChecked() bool {
	if e.checked == nil {
		return false
	}
	return time.Since(time.Unix(0, e.checked.Load())) < poolCheckInterval
}

func (e pooledDB) markChecked() {
	if e.checked != nil {
		e.checked.Store(time.Now().UnixNano())
	}
}

// sizeSQLPool applies a source's connection limit to its pool.
func sizeSQLPool(db *sql.DB, maxConns int) {
	db.SetMaxOpenConns(maxConns)
	db.SetMaxIdleConns(min(sqlPoolMaxIdleConns, maxConns))
	db.SetConnMaxIdleTime(60 * time.Second)
}

// pgPoolConnConfig is the pgx config openSQLDB opens a Postgres pool with, and
// the pool size the connection string asks for.
//
// pgx.ParseConfig passes every key it does not know to the server as a startup
// parameter, and pool_max_conns and the other pool_ keys are pgxpool's, not
// libpq's. PostgreSQL refuses them ("unrecognized configuration parameter")
// and so does PgBouncer ("unsupported startup parameter"), so they are taken
// out here: pool_max_conns sizes the pool, the rest describe a pgxpool this is
// not.
func pgPoolConnConfig(connStr string) (*pgx.ConnConfig, int, error) {
	cfg, _, err := pgxutil.ParseConfig(connStr)
	if err != nil {
		return nil, 0, fmt.Errorf("parse postgres connection config: %w", err)
	}
	maxConns := defaultSQLPoolMaxConns
	if s, ok := cfg.RuntimeParams["pool_max_conns"]; ok {
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil || n <= 0 {
			return nil, 0, fmt.Errorf("parse postgres connection config: pool_max_conns %q is not a positive number", s)
		}
		maxConns = n
	}
	for k := range cfg.RuntimeParams {
		if strings.HasPrefix(k, "pool_") {
			delete(cfg.RuntimeParams, k)
		}
	}
	// Without connect_timeout in the DSN, pgx bounds neither the dial nor the
	// startup exchange after it except by the caller's context, which for the
	// registry's own callers has no deadline: a server that accepts the TCP
	// connection and never answers holds the caller for good.
	if cfg.ConnectTimeout == 0 {
		cfg.ConnectTimeout = pgConnectTimeout
	}
	return cfg, maxConns, nil
}

// openSQLDB builds a *sql.DB for the given canonical driver. For Postgres
// (the pgx stdlib driver) it parses the DSN through pgxutil so that the custom
// pgbouncer/pool_mode markers are stripped and, when a pooler is declared, the
// connection is switched to the exec protocol with the prepared-statement and
// description caches disabled (mandatory for PgBouncer transaction/statement
// pooling). All other drivers fall back to the standard sql.Open behaviour.
// The pool is sized from pool_max_conns, or defaultSQLPoolMaxConns.
func openSQLDB(driverName, connStr string) (*sql.DB, error) {
	if driverName != "pgx" {
		db, err := sql.Open(driverName, connStr)
		if err != nil {
			return nil, err
		}
		sizeSQLPool(db, defaultSQLPoolMaxConns)
		return db, nil
	}
	cfg, maxConns, err := pgPoolConnConfig(connStr)
	if err != nil {
		return nil, err
	}
	db := stdlib.OpenDB(*cfg)
	sizeSQLPool(db, maxConns)
	return db, nil
}

// openPostgresPool is openSQLDB for Postgres, plus the pooler detection the
// shared pgxpool got in PR 436: a connection string that does not say whether
// it goes through PgBouncer is asked, over one connection, and a pool that
// does is reopened without server-side prepared statements. Transaction
// pooling hands each statement to whichever server connection is free, where
// a statement prepared on another one does not exist.
//
// A probe that fails keeps the pool as it is, as pgxutil does: it is the pool
// the operator configured, and its first statement reports the real error.
func openPostgresPool(ctx context.Context, connStr string) (*sql.DB, error) {
	db, err := openSQLDB("pgx", connStr)
	if err != nil || pgxutil.DeclaresPooler(connStr) {
		return db, err
	}
	if !probeSQLProxy(ctx, db) {
		return db, nil
	}
	_ = db.Close()
	cfg, maxConns, err := pgPoolConnConfig(connStr)
	if err != nil {
		return nil, err
	}
	pgxutil.ApplyPoolerSafety(cfg)
	safe := stdlib.OpenDB(*cfg)
	sizeSQLPool(safe, maxConns)
	return safe, nil
}

// probeSQLProxy reports whether db's connections reach PostgreSQL through a
// proxy, false when that cannot be found out.
func probeSQLProxy(ctx context.Context, db *sql.DB) bool {
	ctx, cancel := context.WithTimeout(ctx, pgxutil.ProxyProbeTimeout)
	defer cancel()
	conn, err := db.Conn(ctx)
	if err != nil {
		return false
	}
	defer func() { _ = conn.Close() }()
	proxied := false
	_ = conn.Raw(func(dc any) error {
		c, ok := dc.(*stdlib.Conn)
		if !ok {
			return nil
		}
		proxied, err = pgxutil.ProbeConn(ctx, c.Conn())
		return err
	})
	return err == nil && proxied
}
