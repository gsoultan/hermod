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

// --- A DBA could not tell Hermod's sessions from anyone else's ---
//
// Reported with lookups timing out on a database saturated by long reports:
// pg_stat_activity showed application_name empty on every session, so nobody
// could say which queries were Hermod's, or whether its lookups were running
// at all. Hermod now names its connections unless the DSN, or PGAPPNAME,
// already does. PgBouncer passes application_name through to the server
// connection it assigns, so the name shows there too.

func TestParseConfigNamesTheConnection(t *testing.T) {
	t.Setenv("PGAPPNAME", "")
	cfg, _, err := ParseConfig("postgres://u:p@localhost:5432/db")
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.RuntimeParams["application_name"]; got != ApplicationName {
		t.Errorf("application_name = %q, want %q", got, ApplicationName)
	}
}

func TestParsePoolConfigNamesTheConnection(t *testing.T) {
	t.Setenv("PGAPPNAME", "")
	cfg, _, err := ParsePoolConfig("postgres://u:p@localhost:5432/db")
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.ConnConfig.RuntimeParams["application_name"]; got != ApplicationName {
		t.Errorf("application_name = %q, want %q", got, ApplicationName)
	}
}

func TestParseConfigKeepsAnApplicationNameTheOperatorSet(t *testing.T) {
	t.Setenv("PGAPPNAME", "")
	cfg, _, err := ParseConfig("postgres://u:p@localhost:5432/db?application_name=etl-prod")
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.RuntimeParams["application_name"]; got != "etl-prod" {
		t.Errorf("application_name = %q, want the DSN's own etl-prod", got)
	}

	t.Setenv("PGAPPNAME", "from-env")
	cfg, _, err = ParseConfig("postgres://u:p@localhost:5432/db")
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.RuntimeParams["application_name"]; got != "from-env" {
		t.Errorf("application_name = %q, want PGAPPNAME's from-env", got)
	}
}
