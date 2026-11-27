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
	"context"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ProxyProbeTimeout bounds the one query that asks the server its port.
const ProxyProbeTimeout = 5 * time.Second

// DeclaresPooler reports whether connString says for itself whether it goes
// through a pooler, with pgbouncer=... or pool_mode=..., whatever the value.
// Detection never overrides what the operator wrote.
func DeclaresPooler(connString string) bool {
	if strings.Contains(connString, "://") {
		u, err := url.Parse(connString)
		if err != nil {
			return false
		}
		q := u.Query()
		return q.Has(paramPgBouncer) || q.Has(paramPoolMode)
	}
	for field := range strings.FieldsSeq(connString) {
		key, _, _ := strings.Cut(field, "=")
		switch strings.ToLower(key) {
		case paramPgBouncer, paramPoolMode:
			return true
		}
	}
	return false
}

// behindProxy reports whether a connection dialled at dialled reached a server
// listening on serverPort through something in between. PgBouncer, Pgpool-II
// and gpool's proxy all listen on a port of their own and hand the session to
// a server on another, which answers with its own port when asked. A Unix
// socket reaches the server itself.
//
// A port forwarded to a different one (a container's 15432 to 5432) is taken
// for a proxy too. That costs only the server-side statement cache, which is
// the safe side to be wrong on.
func behindProxy(dialled net.Addr, serverPort int) bool {
	tcp, ok := dialled.(*net.TCPAddr)
	if !ok || tcp == nil {
		return false
	}
	return tcp.Port != serverPort
}

// probeProxy asks the server, over one of pool's connections, which port it
// listens on, and compares it with the port that connection dialled. The
// query uses the simple protocol, so it is safe to run through transaction
// pooling before the answer is known.
func probeProxy(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, ProxyProbeTimeout)
	defer cancel()

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Release()

	return ProbeConn(ctx, conn.Conn())
}

// ProbeConn is the proxy check on one connection, for a caller that holds a
// *pgx.Conn outside a pgxpool (the registry's database/sql pools).
func ProbeConn(ctx context.Context, conn *pgx.Conn) (bool, error) {
	var port int
	if err := conn.QueryRow(ctx, "SELECT current_setting('port')::int", pgx.QueryExecModeSimpleProtocol).Scan(&port); err != nil {
		return false, err
	}
	return behindProxy(conn.PgConn().Conn().RemoteAddr(), port), nil
}
