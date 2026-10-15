// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package repository

import (
	"database/sql"
	"fmt"
	"slices"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	sqlstorage "github.com/gsoultan/hermod/internal/storage/sql"
)

func newStore(t *testing.T) storage.Storage {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.TempDir()+"/lineage.db")
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

func seed(t *testing.T, st storage.Storage) {
	t.Helper()
	ctx := t.Context()
	for i := range 7 {
		if err := st.CreateWorkflow(ctx, storage.Workflow{ID: fmt.Sprintf("a%d", i), Name: fmt.Sprintf("wf a%d", i), VHost: "tenant-a"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.CreateWorkflow(ctx, storage.Workflow{ID: "b0", Name: "b", VHost: "tenant-b"}); err != nil {
		t.Fatal(err)
	}
	for i := range 4 {
		if err := st.CreateSource(ctx, storage.Source{ID: fmt.Sprintf("s%d", i), Name: fmt.Sprintf("src %d", i), Type: "kafka", VHost: "tenant-a",
			Config: hermod.StringMap{"brokers": "b", "topic": "t", "password": "pw"}}); err != nil {
			t.Fatal(err)
		}
		if err := st.CreateSink(ctx, storage.Sink{ID: fmt.Sprintf("k%d", i), Name: fmt.Sprintf("snk %d", i), Type: "kafka", VHost: "tenant-b",
			Config: hermod.StringMap{"brokers": "b", "topic": "t"}}); err != nil {
			t.Fatal(err)
		}
	}
	models, ok := st.(storage.MLModelStore)
	if !ok {
		t.Fatal("the SQL store keeps ML models")
	}
	for _, m := range []storage.MLModel{
		{VHost: "tenant-a", Name: "churn", Backend: "oip", URL: "http://kserve:8080", RemoteModel: "m"},
		{VHost: "tenant-b", Name: "risk", Backend: "oip", URL: "http://kserve:8080", RemoteModel: "m"},
	} {
		if err := models.PutMLModel(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
}

func workflowIDs(ws []storage.Workflow) []string {
	var out []string
	for _, w := range ws {
		out = append(out, w.ID)
	}
	slices.Sort(out)
	return out
}

// Every page is read, however many there are: a lineage built from the first
// page alone would report a source as having no consumers.
func TestLoadReadsEveryPageOfOneVHost(t *testing.T) {
	st := newStore(t)
	seed(t, st)
	r := New(st)
	r.pageSize = 3

	in, err := r.Load(t.Context(), "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := workflowIDs(in.Workflows), []string{"a0", "a1", "a2", "a3", "a4", "a5", "a6"}; !slices.Equal(got, want) {
		t.Fatalf("workflows = %v, want %v", got, want)
	}
	if len(in.Sources) != 4 || len(in.Sinks) != 0 {
		t.Fatalf("sources = %d, sinks = %d", len(in.Sources), len(in.Sinks))
	}
	if in.Sources[0].Config["password"] != "pw" {
		t.Errorf("source config was not decrypted on the way out: %q", in.Sources[0].Config["password"])
	}
	if len(in.Models) != 1 || in.Models[0].Name != "churn" {
		t.Fatalf("models = %+v", in.Models)
	}
}

func TestLoadAllReadsEveryVHost(t *testing.T) {
	st := newStore(t)
	seed(t, st)
	r := New(st)
	r.pageSize = 2

	in, err := r.Load(t.Context(), "all")
	if err != nil {
		t.Fatal(err)
	}
	if len(in.Workflows) != 8 || len(in.Sources) != 4 || len(in.Sinks) != 4 {
		t.Fatalf("workflows = %d, sources = %d, sinks = %d", len(in.Workflows), len(in.Sources), len(in.Sinks))
	}
	var models []string
	for _, m := range in.Models {
		models = append(models, m.VHost+"/"+m.Name)
	}
	slices.Sort(models)
	if want := []string{"tenant-a/churn", "tenant-b/risk"}; !slices.Equal(models, want) {
		t.Fatalf("models = %v, want %v", models, want)
	}
}

func TestLoadRefusesMoreThanTheCap(t *testing.T) {
	st := newStore(t)
	seed(t, st)
	r := New(st)
	r.pageSize, r.maxRecords = 2, 5

	if _, err := r.Load(t.Context(), "tenant-a"); err == nil {
		t.Fatal("7 workflows over a cap of 5 loaded without an error")
	}
}
