package sql

import (
	"database/sql"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	_ "modernc.org/sqlite"
)

// The editor stores a source's sample so that downstream nodes have a field
// list to offer. It used to do that by PUTting the whole source back, which
// meant every unrelated column travelled with it: the object came from a cached
// list, so a config edit made after that list was fetched was reverted by a
// sample capture that had no idea it had happened.
//
// UpdateSourceSample writes the one column it means to, the way
// UpdateSourceStatus and UpdateSourceState already do for theirs.
func sampleStore(t *testing.T) storage.Storage {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	s := NewSQLStorage(db, "sqlite")
	if err := s.Init(t.Context()); err != nil {
		t.Fatalf("init: %v", err)
	}
	return s
}

func TestUpdateSourceSampleTouchesNothingElse(t *testing.T) {
	s := sampleStore(t)

	original := storage.Source{
		ID:       "nightly",
		Name:     "nightly-orders",
		Type:     "batch_sql",
		VHost:    "default",
		Active:   true,
		WorkerID: "worker-1",
		Config: hermod.StringMap{
			"cron":      "0 2 * * *",
			"source_id": "shop",
			"queries":   `["SELECT id FROM orders"]`,
		},
		State: map[string]string{"last_value": "4211"},
	}
	if err := s.CreateSource(t.Context(), original); err != nil {
		t.Fatalf("create: %v", err)
	}

	const captured = `{"after":{"id":1,"region":"eu"},"operation":"snapshot"}`
	if err := s.UpdateSourceSample(t.Context(), "nightly", captured); err != nil {
		t.Fatalf("UpdateSourceSample: %v", err)
	}

	got, err := s.GetSource(t.Context(), "nightly")
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if got.Sample != captured {
		t.Errorf("sample = %q, want %q", got.Sample, captured)
	}
	if got.Name != original.Name {
		t.Errorf("name = %q, want %q", got.Name, original.Name)
	}
	if got.VHost != original.VHost {
		t.Errorf("vhost = %q, want %q", got.VHost, original.VHost)
	}
	if got.WorkerID != original.WorkerID {
		t.Errorf("worker_id = %q, want %q", got.WorkerID, original.WorkerID)
	}
	if !got.Active {
		t.Error("active was cleared by a sample write")
	}
	// The whole point: a sample capture must not revert a config edit it never
	// saw, and must not rewind the cursor.
	for k, want := range original.Config {
		if got.Config[k] != want {
			t.Errorf("config[%q] = %q, want %q", k, got.Config[k], want)
		}
	}
	if got.State["last_value"] != "4211" {
		t.Errorf("state = %v, want last_value 4211", got.State)
	}
}

// Capturing a fresh sample replaces the old one rather than appending to it.
func TestUpdateSourceSampleReplacesTheStoredOne(t *testing.T) {
	s := sampleStore(t)

	if err := s.CreateSource(t.Context(), storage.Source{
		ID: "nightly", Name: "nightly-orders", Type: "batch_sql",
		Sample: `{"after":{"id":1}}`,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	const fresh = `{"after":{"id":2,"region":"us"}}`
	if err := s.UpdateSourceSample(t.Context(), "nightly", fresh); err != nil {
		t.Fatalf("UpdateSourceSample: %v", err)
	}

	got, err := s.GetSource(t.Context(), "nightly")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Sample != fresh {
		t.Errorf("sample = %q, want %q", got.Sample, fresh)
	}
}
