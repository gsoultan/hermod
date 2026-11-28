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
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/gsoultan/hermod/pkg/infra/dbactivity"
	"github.com/jackc/pgx/v5"
)

// ParamLookupReserve is the connection string key for how many of a source's
// Max connections stay free for DB Lookups (see dbactivity.BulkLimit). It is
// Hermod's own, stripped before pgx sees the string, like the pooler markers:
// PostgreSQL and PgBouncer refuse a startup parameter they do not know.
const ParamLookupReserve = "pool_lookup_reserve"

// Database is the physical database connString reaches, as dbactivity keys
// it, with the bulk-query limit its connection string implies: Max
// connections (pool_max_conns) less the lookup reserve. A string that sets no
// pool_max_conns gives limit 0, no gate: Hermod does not know how many server
// connections the database or PgBouncer allows.
func Database(connString string) (*dbactivity.Database, int, error) {
	cleaned, _ := stripPoolerParams(connString)
	cfg, err := pgx.ParseConfig(cleaned)
	if err != nil {
		return nil, 0, err
	}
	maxConns := 0
	if s, ok := cfg.RuntimeParams["pool_max_conns"]; ok {
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil || n <= 0 {
			return nil, 0, fmt.Errorf("pool_max_conns %q is not a positive number", s)
		}
		maxConns = n
	}
	reserve, err := LookupReserve(connString)
	if err != nil {
		return nil, 0, err
	}
	key := dbactivity.Key("postgres", cfg.Host, strconv.Itoa(int(cfg.Port)), cfg.Database, cfg.User)
	return dbactivity.For(key), dbactivity.BulkLimit(maxConns, reserve), nil
}

// LookupReserve is pool_lookup_reserve from connString, -1 when unset (the
// default reserve, a quarter of the connections).
func LookupReserve(connString string) (int, error) {
	raw, ok := "", false
	if strings.Contains(connString, "://") {
		if u, err := url.Parse(connString); err == nil && u.Query().Has(ParamLookupReserve) {
			raw, ok = u.Query().Get(ParamLookupReserve), true
		}
	} else {
		for field := range strings.FieldsSeq(connString) {
			if k, v, found := strings.Cut(field, "="); found && strings.EqualFold(k, ParamLookupReserve) {
				raw, ok = strings.Trim(v, "'\""), true
			}
		}
	}
	if !ok {
		return -1, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%s %q is not a number of connections, 0 or more", ParamLookupReserve, raw)
	}
	return n, nil
}
