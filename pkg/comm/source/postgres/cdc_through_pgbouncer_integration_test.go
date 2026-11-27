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
	"time"

	"github.com/gsoultan/hermod"
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

// Through a PgBouncer that passes replication through (1.23 and later), a CDC
// source streams changes end to end: the slot is made and read through the
// pooler, and an insert arrives. PGBOUNCER_DSN names that PgBouncer and
// POSTGRES_DSN the server behind it; against an older PgBouncer this skips.
func TestCDCStreamsThroughAPgBouncerThatPassesReplication(t *testing.T) {
	pooled := os.Getenv("PGBOUNCER_DSN")
	if pooled == "" {
		t.Skip("integration: set PGBOUNCER_DSN to run")
	}
	f := newCDCFixture(t)
	probe := NewPostgresSource(pooled, "", "", nil, true, "", 0)
	conn, err := probe.openReplicationConn(t.Context())
	_ = probe.Close()
	if err != nil {
		t.Skipf("this PgBouncer does not pass replication through: %v", err)
	}
	_ = conn.Close(t.Context())

	f.dsn = pooled
	f.start(t, t.Context(), false)
	mustExec(t, f.db, "INSERT INTO "+f.table+" (name) VALUES ('through pgbouncer')")

	got, ok := f.collectUntil(t, 20*time.Second, func(m hermod.Message) bool {
		return m.Operation() == hermod.OpCreate
	})
	if !ok {
		t.Fatalf("no insert arrived through PgBouncer; %s", f.why())
	}
	if row := decode(t, got[len(got)-1].After()); row["name"] != "through pgbouncer" {
		t.Errorf("insert through PgBouncer carried %v", row)
	}
}
