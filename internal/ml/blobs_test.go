// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package ml

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
	sqlstorage "github.com/gsoultan/hermod/internal/storage/sql"
	"github.com/gsoultan/hermod/pkg/ml/builtin"
	"github.com/gsoultan/hermod/pkg/ml/inference"
	"github.com/gsoultan/hermod/pkg/ml/worker"

	_ "modernc.org/sqlite"
)

// sqliteStore is Hermod's metadata store on SQLite, as two replicas share
// one database.
func sqliteStore(t *testing.T) storage.Storage {
	t.Helper()
	db, err := sql.Open("sqlite", fmt.Sprintf("file:blobs_%s?mode=memory&cache=shared", t.Name()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	st := sqlstorage.NewSQLStorage(db, "sqlite")
	if err := st.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	return st
}

// TestReplicasOverOneMetadataStoreServeEachOthersModels is two Hermod
// servers with nothing on disk in common: one fills a dataset and trains,
// the other, started afresh, scores the model.
func TestReplicasOverOneMetadataStoreServeEachOthersModels(t *testing.T) {
	st := sqliteStore(t)
	replica := func() *builtin.Engine {
		e, err := builtin.New(builtin.Config{Blobs: BuiltinBlobs(func() any { return st }, nil), Workers: 2})
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	a := replica()
	rows := make([]map[string]any, 120)
	for i := range rows {
		plan := []string{"basic", "pro"}[i%2]
		rows[i] = map[string]any{"age": float64(20 + i%40), "plan": plan, "churned": map[string]string{"basic": "yes", "pro": "no"}[plan]}
	}
	if _, err := a.Client().AppendRows(t.Context(), "v", "customers", rows, true); err != nil {
		t.Fatal(err)
	}
	v, err := a.Client().Train(t.Context(), "v", "churn", worker.TrainSpec{Dataset: "customers", Target: "churned", Algorithm: "linear"})
	if err != nil {
		t.Fatal(err)
	}

	b := replica()
	out, err := b.Predict(t.Context(), "v", "churn", v.Version, []inference.Row{{"age": 30.0, "plan": "basic"}})
	if err != nil || out[0]["label"] != "yes" {
		t.Errorf("the other replica predicts %v, %v", out, err)
	}
	if info, err := b.Client().Dataset(t.Context(), "v", "customers"); err != nil || info.Rows != 120 {
		t.Errorf("the other replica sees the dataset as %+v, %v", info, err)
	}
}

func TestBuiltinBlobsSpeakTheEnginesErrors(t *testing.T) {
	b := BuiltinBlobs(func() any { return sqliteStore(t) }, nil)
	ctx := t.Context()
	if _, err := b.Get(ctx, "v", "a/b"); !errors.Is(err, builtin.ErrBlobNotFound) {
		t.Errorf("Get of nothing = %v, want ErrBlobNotFound", err)
	}
	if err := b.Create(ctx, "v", "a/b", []byte("1")); err != nil {
		t.Fatal(err)
	}
	if err := b.Create(ctx, "v", "a/b", []byte("2")); !errors.Is(err, builtin.ErrBlobExists) {
		t.Errorf("a second Create = %v, want ErrBlobExists", err)
	}
	if err := b.Put(ctx, "v", "big", make([]byte, storage.MaxMLBlobBytes+1)); !errors.Is(err, builtin.ErrBlobTooLarge) {
		t.Errorf("Put over the limit = %v, want ErrBlobTooLarge", err)
	}
}

func TestBuiltinBlobsNeedAStoreThatKeepsThem(t *testing.T) {
	none := BuiltinBlobs(func() any { return nil }, nil)
	if _, err := none.Get(t.Context(), "v", "a"); err == nil || !strings.Contains(err.Error(), "storage") {
		t.Errorf("with no store yet: %v", err)
	}
	// A store that keeps no blobs falls back to the directory, if one is set.
	dir, err := builtin.DirBlobs(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = builtin.CloseBlobs(dir) })
	fallback := BuiltinBlobs(func() any { return newMemStore() }, dir)
	if err := fallback.Put(t.Context(), "v", "a", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if got, err := dir.Get(t.Context(), "v", "a"); err != nil || string(got) != "x" {
		t.Errorf("the fallback directory holds %q, %v", got, err)
	}
	if _, err := BuiltinBlobs(func() any { return newMemStore() }, nil).Get(t.Context(), "v", "a"); err == nil {
		t.Error("a store that keeps no blobs, with no fallback, was used")
	}
}
