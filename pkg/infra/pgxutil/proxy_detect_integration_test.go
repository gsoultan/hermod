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
// +build integration

package pgxutil

import (
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
)

// A pool opened through PgBouncer with no pgbouncer=true in its connection
// string used to keep pgx's default, server-side prepared statements, which
// transaction pooling breaks. The pooler now finds the proxy itself.
//
//	HERMOD_INTEGRATION=1 \
//	POSTGRES_DSN='postgres://postgres@127.0.0.1:5432/postgres?sslmode=disable' \
//	PGBOUNCER_DSN='postgres://postgres@127.0.0.1:6432/postgres?sslmode=disable' \
//	go test -tags integration -run TestPoolerDetects ./pkg/infra/pgxutil/
func TestPoolerDetectsAProxyWithoutAMarker(t *testing.T) {
	direct, proxied := os.Getenv("POSTGRES_DSN"), os.Getenv("PGBOUNCER_DSN")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || direct == "" || proxied == "" {
		t.Skip("integration: set HERMOD_INTEGRATION=1, POSTGRES_DSN and PGBOUNCER_DSN (no pgbouncer marker) to run")
	}
	pooler := NewPooler()
	t.Cleanup(pooler.Close)

	for name, tc := range map[string]struct {
		dsn       string
		wantProxy bool
	}{
		"through PgBouncer": {proxied, true},
		"direct":            {direct, false},
	} {
		t.Run(name, func(t *testing.T) {
			pool, err := pooler.Get(t.Context(), tc.dsn)
			if err != nil {
				t.Fatal(err)
			}
			if got := pooler.Pooled(tc.dsn); got != tc.wantProxy {
				t.Fatalf("Pooled = %v, want %v", got, tc.wantProxy)
			}
			mode := pool.Config().ConnConfig.DefaultQueryExecMode
			if tc.wantProxy && mode != pgx.QueryExecModeExec {
				t.Fatalf("query mode through a proxy = %v, want exec (no server-side prepared statements)", mode)
			}
			if !tc.wantProxy && mode == pgx.QueryExecModeExec {
				t.Fatal("a direct connection lost its prepared statements")
			}
			// Many statements on many connections, as transaction pooling
			// shuffles them, must all succeed.
			for i := range 200 {
				var n int
				if err := pool.QueryRow(t.Context(), "SELECT $1::int + 1", i).Scan(&n); err != nil {
					t.Fatalf("query %d: %v", i, err)
				}
			}
		})
	}
}
