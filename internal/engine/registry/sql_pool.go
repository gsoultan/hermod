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
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/infra/dbactivity"
	"github.com/gsoultan/hermod/pkg/infra/pgxutil"
	"github.com/gsoultan/hermod/pkg/infra/sqlutil"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// defaultSQLPoolMaxConns is the size of the database/sql pool a source gets
// for DB Lookup, Execute SQL and the other per-message statements, when its
// connection string sets no pool_max_conns (the source form's Max
// connections). It is per source, per worker process.
const defaultSQLPoolMaxConns = 20

// sqlStepsApplicationName is the application_name of this pool's
// connections, apart from the sources' and sinks' "hermod", so pg_stat_activity
// tells a lookup or SQL step from a poll or a write.
const sqlStepsApplicationName = "hermod-sql-steps"

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
	cfg, _, err := pgxutil.ParseConfigAs(connStr, sqlStepsApplicationName)
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

// bindSQLPool records which physical database db reaches (dbactivity), so DB
// Lookup and Execute SQL can find what else Hermod is running there, and
// applies the bulk limit the connection string implies. A Postgres string is
// keyed by what the server sees; any other driver by its whole string, which
// still joins every pool opened with the same one.
func bindSQLPool(db *sql.DB, driverName, connStr string) {
	if driverName == "pgx" {
		if d, limit, err := pgxutil.Database(connStr); err == nil {
			if limit > 0 {
				d.SetLimit(limit)
			}
			dbactivity.Bind(db, d)
			reserve, _ := pgxutil.LookupReserve(connStr)
			startTuner(db, connStr, d, reserve)
			return
		}
	}
	sum := sha256.Sum256([]byte(connStr))
	dbactivity.Bind(db, dbactivity.For(dbactivity.Key(driverName, "", "", "", hex.EncodeToString(sum[:8]))))
}

// closeSQLPool closes a pool, stopping its tuner and forgetting its
// dbactivity binding.
func closeSQLPool(db *sql.DB) {
	if v, ok := tuners.LoadAndDelete(db); ok {
		if stop, ok := v.(func()); ok {
			stop()
		}
	}
	dbactivity.Unbind(db)
	_ = db.Close()
}

// tunerInterval overrides how often the canary runs; tests shorten it.
var tunerInterval time.Duration

// tuners holds, per open Postgres pool, the func that stops its tuner.
var tuners sync.Map

// startTuner starts the goroutine that learns how many server connections
// Hermod really gets on d (see dbactivity.Tuner), so lookups keep connections
// free behind PgBouncer without anyone copying its pool_size into the source.
// The pool owns it: closeSQLPool stops it and waits for it.
//
// The canary has a connection of its own, outside the pool: when the pool is
// full the canary would otherwise queue inside Hermod and learn nothing about
// PgBouncer. A Postgres string the canary cannot open gets no tuner.
func startTuner(db *sql.DB, connStr string, d *dbactivity.Database, reserve int) {
	probeDB, err := openCanary(connStr)
	if err != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	tu := dbactivity.NewTuner(d, canary(probeDB), db.Stats().MaxOpenConnections, reserve)
	if tunerInterval > 0 {
		tu.Interval = tunerInterval
	}
	go func() {
		defer close(done)
		defer func() { _ = probeDB.Close() }()
		tu.Run(ctx)
	}()
	tuners.Store(db, func() { cancel(); <-done })
}

// openCanary is a one-connection pool for the canary. It never prepares a
// statement, so it is safe through transaction pooling whatever the
// connection string says.
func openCanary(connStr string) (*sql.DB, error) {
	cfg, _, err := pgPoolConnConfig(connStr)
	if err != nil {
		return nil, err
	}
	pgxutil.ApplyPoolerSafety(cfg)
	probeDB := stdlib.OpenDB(*cfg)
	probeDB.SetMaxOpenConns(1)
	probeDB.SetMaxIdleConns(1)
	return probeDB, nil
}

// canary is the tuner's probe: SELECT 1 on its own connection. It costs the
// server nothing, so a slow answer was a wait for a server connection.
func canary(probeDB *sql.DB) dbactivity.Probe {
	return func(ctx context.Context) dbactivity.ProbeResult {
		var one int
		err := probeDB.QueryRowContext(ctx, "SELECT 1").Scan(&one)
		switch {
		case err == nil, errors.Is(err, context.DeadlineExceeded):
			return dbactivity.ProbeAnswered
		case errors.Is(sqlutil.MarkBusy(err), hermod.ErrBusy):
			return dbactivity.ProbeRefused
		default:
			return dbactivity.ProbeSkipped
		}
	}
}

// DatabaseSnapshot is what Hermod is running on the database src reaches,
// and what it learned about it, as dbactivity tracks it. It reads memory
// only; nothing connects. False for a source that is not PostgreSQL, whose
// activity is not tracked by server identity.
func (r *Registry) DatabaseSnapshot(ctx context.Context, src storage.Source) (dbactivity.Snapshot, bool) {
	connStr, driver, err := r.SourceConnString(ctx, src)
	if err != nil || driver != "pgx" {
		return dbactivity.Snapshot{}, false
	}
	d, _, err := pgxutil.Database(connStr)
	if err != nil {
		return dbactivity.Snapshot{}, false
	}
	return d.Snapshot(), true
}
