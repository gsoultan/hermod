// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package registry

import (
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/infra/pgxutil"
)

// The Tuning page reads what Hermod learned about a source's database from
// the same dbactivity entry its pools use: keyed by what the server sees, so
// a second source with the same host, database and user reads the same one.
func TestDatabaseSnapshotReadsTheSharedEntry(t *testing.T) {
	r := newTestRegistry()
	cfg := map[string]string{"host": "pgb.snapshot.test", "port": "6433", "dbname": "athena", "user": "etl", "password": "x"}
	connStr, driver, err := r.SourceConnString(t.Context(), storage.Source{Type: "postgres", Config: cfg})
	if err != nil || driver != "pgx" {
		t.Fatalf("conn string: %v, %s", err, driver)
	}
	d, _, err := pgxutil.Database(connStr)
	if err != nil {
		t.Fatal(err)
	}
	d.Learn(12, -1)

	snap, ok := r.DatabaseSnapshot(t.Context(), storage.Source{Type: "postgres", Config: cfg})
	if !ok || snap.Learned != 12 {
		t.Errorf("snapshot = %+v, %v", snap, ok)
	}
	if _, ok := r.DatabaseSnapshot(t.Context(), storage.Source{Type: "mysql", Config: cfg}); ok {
		t.Error("a MySQL source was read")
	}
}
