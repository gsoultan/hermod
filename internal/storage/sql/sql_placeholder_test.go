// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package sql

import "testing"

func TestPrepareQuery_Types(t *testing.T) {
	s := &sqlStorage{driver: "pgx"}
	in := "CREATE TABLE webhook_requests (body BLOB)"
	want := "CREATE TABLE webhook_requests (body BYTEA)"
	got := s.prepareQuery(in)
	if got != want {
		t.Fatalf("pgx types BLOB: want %q, got %q", want, got)
	}

	in = "ALTER TABLE t ADD COLUMN c REAL"
	want = "ALTER TABLE t ADD COLUMN c DOUBLE PRECISION"
	got = s.prepareQuery(in)
	if got != want {
		t.Fatalf("pgx types REAL: want %q, got %q", want, got)
	}

	s.driver = "sqlite"
	in = "CREATE TABLE webhook_requests (body BLOB)"
	want = in
	got = s.prepareQuery(in)
	if got != want {
		t.Fatalf("sqlite types should be unchanged: want %q, got %q", want, got)
	}
}

func TestPreparePlaceholders_PGX_Extra(t *testing.T) {
	s := &sqlStorage{driver: "pgx"}
	in := "SELECT * FROM t WHERE a = ? AND b = ? LIMIT ? OFFSET ?"
	want := "SELECT * FROM t WHERE a = $1 AND b = $2 LIMIT $3 OFFSET $4"
	got := s.preparePlaceholders(in)
	if got != want {
		t.Fatalf("pgx placeholders: want %q, got %q", want, got)
	}
}

func TestPreparePlaceholders_SQLServer_Extra(t *testing.T) {
	s := &sqlStorage{driver: "sqlserver"}
	in := "INSERT INTO t (a,b,c) VALUES (?, ?, ?)"
	want := "INSERT INTO t (a,b,c) VALUES (@p1, @p2, @p3)"
	got := s.preparePlaceholders(in)
	if got != want {
		t.Fatalf("sqlserver placeholders: want %q, got %q", want, got)
	}
}

func TestPreparePlaceholders_Default_Extra(t *testing.T) {
	s := &sqlStorage{driver: "sqlite"}
	in := "UPDATE t SET a = ? WHERE id = ?"
	want := in
	got := s.preparePlaceholders(in)
	if got != want {
		t.Fatalf("default placeholders should be unchanged: want %q, got %q", want, got)
	}
}
