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
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/gsoultan/hermod/pkg/infra/pgxutil"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// readPooler asks PgBouncer's console for the pool Hermod uses, with the
// source's own credentials. It succeeds only when that user is in
// stats_users or admin_users; otherwise it says why, without the
// credentials, and the diagnosis goes on without it.
func readPooler(ctx context.Context, connStr string) *PoolerStats {
	cfg, _, err := pgxutil.ParseConfig(connStr)
	if err != nil {
		return &PoolerStats{Reason: "The connection string could not be read"}
	}
	db, user := cfg.Database, cfg.User
	for k := range cfg.RuntimeParams {
		if k != "application_name" {
			delete(cfg.RuntimeParams, k)
		}
	}
	cfg.Database = "pgbouncer"
	// The console speaks only the simple query protocol.
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return &PoolerStats{Reason: refusal(err)}
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()

	p := &PoolerStats{Readable: true}
	if err := showRow(ctx, conn, "SHOW POOLS", db, user, func(get func(string) string) {
		p.ClientActive = atoi(get("cl_active"))
		p.ClientWaiting = atoi(get("cl_waiting"))
		p.ServerActive = atoi(get("sv_active"))
		p.ServerIdle = atoi(get("sv_idle"))
		p.MaxWaitSeconds = float64(atoi(get("maxwait"))) + float64(atoi(get("maxwait_us")))/1e6
	}); err != nil {
		return &PoolerStats{Reason: refusal(err)}
	}
	_ = showRow(ctx, conn, "SHOW DATABASES", db, "", func(get func(string) string) {
		p.PoolSize = atoi(get("pool_size"))
	})
	return p
}

// showRow runs a console SHOW and hands fn the row for database db (and user,
// when given).
func showRow(ctx context.Context, conn *pgx.Conn, show, db, user string, fn func(get func(string) string)) error {
	rows, err := conn.Query(ctx, show)
	if err != nil {
		return err
	}
	defer rows.Close()
	cols := rows.FieldDescriptions()
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return err
		}
		get := func(name string) string {
			for i, c := range cols {
				if c.Name == name && i < len(vals) && vals[i] != nil {
					return fmt.Sprint(vals[i])
				}
			}
			return ""
		}
		// SHOW DATABASES names the database "name"; SHOW POOLS "database".
		if (get("database") == db || get("name") == db) && (user == "" || get("user") == user) {
			fn(get)
			return nil
		}
	}
	return rows.Err()
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

// refusal says why the console could not be read, by SQLSTATE and never with
// the connection details, which carry the password.
func refusal(err error) string {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		return "PgBouncer refused to show its pool figures to this user (SQLSTATE " + pg.Code + ")"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "PgBouncer's console did not answer in time"
	}
	return "PgBouncer's console could not be reached"
}
