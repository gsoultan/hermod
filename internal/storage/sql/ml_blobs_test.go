// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package sql

import (
	"database/sql"
	"os"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/internal/storage/mlblobtest"
)

func TestMLBlobsKeepTheContract(t *testing.T) {
	withKey(t, "ml-blob-test-key")
	var st storage.Storage = newRotationStorage(t)
	bs, ok := st.(storage.MLBlobStore)
	if !ok {
		t.Fatal("the SQL store does not implement MLBlobStore")
	}
	mlblobtest.Run(t, bs, "tenant-a")
}

// TestMLBlobsKeepTheContractOnPostgres runs the same contract on PostgreSQL,
// where data is BYTEA and the placeholders are rewritten.
func TestMLBlobsKeepTheContractOnPostgres(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || dsn == "" {
		t.Skip("integration: set HERMOD_INTEGRATION=1 and POSTGRES_DSN to enable")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("POSTGRES_DSN names a server that could not be opened (%s): %v", dsn, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := NewSQLStorage(db, "pgx")
	if err := s.Init(t.Context()); err != nil {
		t.Fatalf("POSTGRES_DSN names a server that could not be initialised (%s): %v", dsn, err)
	}
	vhost := "blobs-" + t.Name()
	bs := s.(storage.MLBlobStore)
	t.Cleanup(func() {
		_, _ = bs.DeleteMLBlobs(t.Context(), vhost, "")
		_, _ = bs.DeleteMLBlobs(t.Context(), vhost+"-other", "")
	})
	mlblobtest.Run(t, bs, vhost)
}

// TestDeletingAVHostDeletesItsMLBlobs: a later vhost of the same name must not
// find the old one's datasets or models.
func TestDeletingAVHostDeletesItsMLBlobs(t *testing.T) {
	withKey(t, "ml-blob-test-key")
	st := newRotationStorage(t)
	ctx := t.Context()
	if err := st.CreateVHost(ctx, storage.VHost{ID: "vh-1", Name: "tenant-a"}); err != nil {
		t.Fatal(err)
	}
	var bs storage.MLBlobStore = st
	if err := bs.PutMLBlob(ctx, "tenant-a", "models/m/b1/meta", []byte("{}")); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteVHost(ctx, "vh-1"); err != nil {
		t.Fatal(err)
	}
	if got, _ := bs.ListMLBlobs(ctx, "tenant-a", "", false); len(got) != 0 {
		t.Errorf("the deleted vhost's blobs are still there: %+v", got)
	}
}
