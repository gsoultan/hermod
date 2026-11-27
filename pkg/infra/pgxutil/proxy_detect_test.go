// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package pgxutil

import (
	"net"
	"testing"
)

// Hermod used to treat a connection as going through PgBouncer only when its
// connection string said so with pgbouncer=true or pool_mode=..., or the form
// used port 6432. Anyone who left the marker off a pooler on another port got
// server-side prepared statements through transaction pooling, which PgBouncer
// breaks. Hermod now asks the server which port it listens on: a proxy in
// between answers with the server's port, not the one Hermod dialled.
func TestProxyDetectionDecision(t *testing.T) {
	cases := []struct {
		name       string
		dialled    net.Addr
		serverPort int
		want       bool
	}{
		{"same port is direct", &net.TCPAddr{IP: net.IPv4(10, 0, 0, 1), Port: 5432}, 5432, false},
		{"different port is a proxy", &net.TCPAddr{IP: net.IPv4(10, 0, 0, 1), Port: 6543}, 5432, true},
		{"a Unix socket reaches the server itself", &net.UnixAddr{Name: "/run/postgresql/.s.PGSQL.5432", Net: "unix"}, 6432, false},
		{"no address is not a proxy", nil, 5432, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := behindProxy(tc.dialled, tc.serverPort); got != tc.want {
				t.Fatalf("behindProxy(%v, server port %d) = %v, want %v", tc.dialled, tc.serverPort, got, tc.want)
			}
		})
	}
}

// An explicit marker is the operator's word and is never second-guessed by
// detection, either way: pgbouncer=false keeps prepared statements on even
// through a proxy that tracks them (PgBouncer 1.21+ with
// max_prepared_statements), and pgbouncer=true needs no probe.
func TestExplicitPoolerMarkerSkipsDetection(t *testing.T) {
	for dsn, want := range map[string]bool{
		"postgres://u@db:5432/app":                    false,
		"postgres://u@db:5432/app?pgbouncer=false":    true,
		"postgres://u@db:5432/app?pgbouncer=true":     true,
		"postgres://u@db:5432/app?pool_mode=session":  true,
		"host=db port=5432 dbname=app pgbouncer=true": true,
		"host=db port=5432 dbname=app":                false,
	} {
		if got := DeclaresPooler(dsn); got != want {
			t.Errorf("DeclaresPooler(%q) = %v, want %v", dsn, got, want)
		}
	}
}
