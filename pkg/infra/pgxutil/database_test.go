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

import "testing"

func TestDatabaseKeysByServerAndSetsTheBulkLimit(t *testing.T) {
	a, limit, err := Database("postgres://hermod:x@pgb.example:6432/athena_etl_db?pool_max_conns=12&pgbouncer=true")
	if err != nil {
		t.Fatal(err)
	}
	if limit != 9 {
		t.Errorf("limit = %d with Max connections 12, want 9 (3 kept for lookups)", limit)
	}
	// Another Hermod source on the same database, different query, other password.
	b, _, err := Database("postgres://hermod:y@pgb.example:6432/athena_etl_db?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Error("two connection strings to one database got different trackers")
	}
	c, _, _ := Database("postgres://other:x@pgb.example:6432/athena_etl_db")
	if c == a {
		t.Error("a different user shares the tracker; PgBouncer pools by user")
	}
}

func TestDatabaseWithoutMaxConnectionsHasNoGate(t *testing.T) {
	_, limit, err := Database("postgres://hermod@h:5432/db")
	if err != nil {
		t.Fatal(err)
	}
	if limit != 0 {
		t.Errorf("limit = %d with no Max connections, want 0 (unknown: no gate)", limit)
	}
}

func TestLookupReserveIsReadAndNeverSentToTheServer(t *testing.T) {
	_, limit, err := Database("postgres://hermod@h:5432/db?pool_max_conns=12&pool_lookup_reserve=6")
	if err != nil {
		t.Fatal(err)
	}
	if limit != 6 {
		t.Errorf("limit = %d, want 12-6", limit)
	}
	cfg, _, err := ParsePoolConfig("postgres://hermod@h:5432/db?pool_lookup_reserve=6")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.ConnConfig.RuntimeParams[ParamLookupReserve]; ok {
		t.Error("pool_lookup_reserve would be sent to the server as a startup parameter")
	}
	cfg2, _, err := ParseConfig("host=h user=hermod pool_lookup_reserve=2")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg2.RuntimeParams[ParamLookupReserve]; ok {
		t.Error("keyword form: pool_lookup_reserve would be sent to the server")
	}
	if _, _, err := Database("postgres://hermod@h/db?pool_lookup_reserve=-1"); err == nil {
		t.Error("a negative reserve was accepted")
	}
}
