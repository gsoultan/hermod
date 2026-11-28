// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package dbdiag

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gsoultan/hermod/pkg/infra/pgxutil"
)

// Timeout bounds one diagnosis on the server. Every query reads catalog
// views, so it takes milliseconds; the bound is for a server that is not
// answering at all.
const Timeout = 5 * time.Second

const limitsSQL = `SELECT current_setting('server_version'),
	current_setting('port')::int,
	current_setting('max_connections')::int,
	current_setting('superuser_reserved_connections')::int,
	(SELECT count(*) FROM pg_stat_activity WHERE backend_type = 'client backend'),
	coalesce((SELECT rolconnlimit FROM pg_roles WHERE rolname = current_user), -1),
	coalesce((SELECT datconnlimit FROM pg_database WHERE datname = current_database()), -1),
	(SELECT count(*) FROM pg_stat_activity WHERE usename = current_user AND backend_type = 'client backend'),
	(SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND backend_type = 'client backend')`

// The query text is read only to take its leading keywords (command); it is
// cut short here so a huge statement is not shipped across for that.
const sessionsSQL = `SELECT pid, coalesce(usename, ''), coalesce(application_name, ''),
	coalesce(state, ''), coalesce(wait_event_type, ''), coalesce(wait_event, ''),
	coalesce(extract(epoch FROM now() - query_start), 0)::float8,
	coalesce(extract(epoch FROM now() - xact_start), 0)::float8,
	coalesce(extract(epoch FROM now() - state_change), 0)::float8,
	coalesce(left(query, 120), ''),
	array_to_string(pg_blocking_pids(pid), ',')
FROM pg_stat_activity
WHERE datname = current_database() AND backend_type = 'client backend' AND pid <> pg_backend_pid()`

// Run diagnoses the PostgreSQL database db reaches. connStr is the source's
// connection string: it gives the port Hermod dials, and, behind PgBouncer,
// the credentials for a look at its console. Nothing is changed: the reads run
// in a read-only transaction.
func Run(ctx context.Context, db *sql.DB, connStr string) (Report, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	s, err := read(ctx, db)
	if err != nil {
		return Report{}, err
	}
	dialed := dialedPort(connStr)
	if dialed > 0 && dialed != s.ServerPort {
		s.Pooler = readPooler(ctx, connStr)
	}
	r := assess(s, dialed)
	r.CheckedAt = time.Now().UTC()
	return r, nil
}

func read(ctx context.Context, db *sql.DB) (snapshot, error) {
	var s snapshot
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return s, fmt.Errorf("could not reach the database: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, "SET LOCAL statement_timeout = '5s'"); err != nil {
		return s, fmt.Errorf("could not reach the database: %w", err)
	}
	if err := tx.QueryRowContext(ctx, limitsSQL).Scan(&s.ServerVersion, &s.ServerPort, &s.MaxConns, &s.Reserved,
		&s.InUse, &s.RoleLimit, &s.DBLimit, &s.OwnUser, &s.DBConns); err != nil {
		return s, fmt.Errorf("could not read the server's limits: %w", err)
	}
	// "16.15 (Ubuntu 16.15-0ubuntu0.24.04.1)" says 16.15.
	if f := strings.Fields(s.ServerVersion); len(f) > 0 {
		s.ServerVersion = f[0]
	}
	rows, err := tx.QueryContext(ctx, sessionsSQL)
	if err != nil {
		return s, fmt.Errorf("could not read pg_stat_activity: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var ss Session
		var query, blocked string
		if err := rows.Scan(&ss.PID, &ss.User, &ss.App, &ss.State, &ss.WaitType, &ss.WaitEvent,
			&ss.QuerySeconds, &ss.XactSeconds, &ss.StateSeconds, &query, &blocked); err != nil {
			return s, fmt.Errorf("could not read pg_stat_activity: %w", err)
		}
		// Without pg_read_all_stats another user's row has no state and its
		// query reads "<insufficient privilege>".
		ss.Hidden = ss.State == "" && strings.HasPrefix(query, "<insufficient")
		ss.Command = command(query)
		ss.BlockedBy = pids(blocked)
		s.Sessions = append(s.Sessions, ss)
	}
	return s, rows.Err()
}

func pids(s string) []int {
	var out []int
	for f := range strings.SplitSeq(s, ",") {
		if n, err := strconv.Atoi(strings.TrimSpace(f)); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// dialedPort is the port connStr dials, 0 when it cannot be read.
func dialedPort(connStr string) int {
	cfg, _, err := pgxutil.ParseConfig(connStr)
	if err != nil {
		return 0
	}
	return int(cfg.Port)
}

// ErrUnsupported is returned for a database other than PostgreSQL.
var ErrUnsupported = errors.New("only PostgreSQL databases can be diagnosed so far")
