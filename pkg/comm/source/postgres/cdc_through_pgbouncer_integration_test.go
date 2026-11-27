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

package postgres

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// A CDC source whose connection string went through PgBouncer was refused
// before it tried: "CDC requires a direct Postgres connection". That was true
// once, but PgBouncer 1.23 and later pass a replication connection through to
// the server unpooled, one server connection per client, so the refusal kept
// working setups from running. Hermod now tries, and only a PgBouncer that
// does refuse replication gets an error, which names the version that can.
//
// PGBOUNCER_DSN names a PgBouncer older than 1.23 (1.22 is what Ubuntu 24.04
// ships); against a newer one the connection succeeds and the test skips.
func TestCDCThroughPgBouncerIsTriedNotRefused(t *testing.T) {
	dsn := os.Getenv("PGBOUNCER_DSN")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || dsn == "" {
		t.Skip("integration: set HERMOD_INTEGRATION=1 and PGBOUNCER_DSN to run")
	}
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	src := NewPostgresSource(dsn+sep+"pgbouncer=true", "", "", nil, true, "", 0)
	t.Cleanup(func() { _ = src.Close() })

	conn, err := src.openReplicationConn(t.Context())
	if err == nil {
		_ = conn.Close(t.Context())
		t.Skip("this PgBouncer passes replication through")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("openReplicationConn = %v; want PgBouncer's own refusal, not a refusal made before trying", err)
	}
	if !strings.Contains(err.Error(), "1.23") {
		t.Fatalf("openReplicationConn = %v; want it to name PgBouncer 1.23, which passes replication through", err)
	}
}
