package http

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
	sqlstorage "github.com/gsoultan/hermod/internal/storage/sql"
)

// This file is the reachability test for export/import: it starts from real
// storage, goes out through the HTTP export, back in through the HTTP import on
// a *second* store, and asserts on what the second store holds.
//
// The mocks in workflow_export_completeness_test.go cannot see the two things
// that only the real storage layer does. Configuration is encrypted at rest and
// decrypted on read, so a credential makes a full encrypt -> decrypt -> JSON ->
// encrypt round trip on its way between instances; and a source's Config is a
// hermod.StringMap that is marshalled to a JSON column and back. Either could
// lose a value while every handler test still passed.

func newSQLiteStore(t *testing.T, name string) storage.Storage {
	t.Helper()
	db, err := sql.Open("sqlite", "file:wfexport_"+t.Name()+"_"+name+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	store := sqlstorage.NewSQLStorage(db, "sqlite")
	if err := store.Init(t.Context()); err != nil {
		t.Fatalf("init %s store: %v", name, err)
	}
	return store
}

func handlerFor(store storage.Storage) *http.ServeMux {
	h := &WorkflowHandler{Handler: &handlers.Handler{Storage: store, LogStorage: store}}
	mux := http.NewServeMux()
	h.RegisterWorkflowRoutes(mux)
	return mux
}

// TestExportImportRoundTripAcrossInstances moves a workflow between two real
// metadata stores the way an operator does, and asserts that the destination
// can resolve every reference the workflow makes — including the one a
// transformation node holds in its config, which the export used not to collect.
func TestExportImportRoundTripAcrossInstances(t *testing.T) {
	origin := newSQLiteStore(t, "origin")
	target := newSQLiteStore(t, "target")

	ctx := t.Context()

	mainSrc := storage.Source{
		ID: "src-main", Name: "Orders CDC", Type: "postgres", VHost: "default", Active: true,
		Config: hermod.StringMap{
			"host":         "orders.internal",
			"db_password":  "s3cret-orders",
			"publication":  "hermod_orders",
			"use_cdc":      "true",
			"replica_slot": "hermod_slot",
		},
		State: map[string]string{"lsn": "0/4A2F1B8"},
	}
	lookupSrc := storage.Source{
		ID: "src-lookup", Name: "Customer master", Type: "mysql", VHost: "default", Active: true,
		Config: hermod.StringMap{"host": "crm.internal", "db_password": "s3cret-crm", "use_cdc": "false"},
	}
	dlqSink := storage.Sink{
		ID: "snk-dlq", Name: "Dead letters", Type: "postgres", VHost: "default", Active: true,
		Config: hermod.StringMap{"host": "dlq.internal", "db_password": "s3cret-dlq"},
	}
	outSink := storage.Sink{
		ID: "snk-out", Name: "Warehouse", Type: "postgres", VHost: "default", Active: true,
		Config: hermod.StringMap{"host": "dw.internal", "db_password": "s3cret-dw"},
	}
	for _, src := range []storage.Source{mainSrc, lookupSrc} {
		if err := origin.CreateSource(ctx, src); err != nil {
			t.Fatalf("seed source %s: %v", src.ID, err)
		}
	}
	for _, snk := range []storage.Sink{dlqSink, outSink} {
		if err := origin.CreateSink(ctx, snk); err != nil {
			t.Fatalf("seed sink %s: %v", snk.ID, err)
		}
	}

	wf := storage.Workflow{
		ID: "wf-orders", Name: "Orders to warehouse", VHost: "default", Active: true,
		DeadLetterSinkID: "snk-dlq",
		Nodes: []storage.WorkflowNode{
			{ID: "n1", Type: "source", RefID: "src-main"},
			{ID: "n2", Type: "transformation", Config: map[string]any{
				"transType":   "db_lookup",
				"sourceId":    "src-lookup",
				"table":       "customers",
				"keyColumn":   "id",
				"targetField": "customer",
			}},
			{ID: "n3", Type: "sink", RefID: "snk-out"},
		},
		Edges: []storage.WorkflowEdge{
			{ID: "e1", SourceID: "n1", TargetID: "n2"},
			{ID: "e2", SourceID: "n2", TargetID: "n3"},
		},
	}
	if err := origin.CreateWorkflow(ctx, wf); err != nil {
		t.Fatalf("seed workflow: %v", err)
	}

	// Export from the origin.
	exportReq := httptest.NewRequestWithContext(ctx, "GET", "/api/workflows/wf-orders/export", nil)
	exportRR := httptest.NewRecorder()
	handlerFor(origin).ServeHTTP(exportRR, exportReq)
	if exportRR.Code != http.StatusOK {
		t.Fatalf("export: HTTP %d, body %s", exportRR.Code, exportRR.Body.String())
	}
	exported := exportRR.Body.Bytes()

	// Import into the target — the bytes as downloaded, nothing rewritten.
	importReq := httptest.NewRequestWithContext(ctx, "POST", "/api/workflows/import", bytes.NewReader(exported))
	importReq.Header.Set("Content-Type", "application/json")
	importRR := httptest.NewRecorder()
	handlerFor(target).ServeHTTP(importRR, importReq)
	if importRR.Code != http.StatusCreated {
		t.Fatalf("import: HTTP %d, body %s", importRR.Code, importRR.Body.String())
	}

	gotWF, err := target.GetWorkflow(ctx, "wf-orders")
	if err != nil {
		t.Fatalf("the imported workflow is not in the target store: %v", err)
	}
	if gotWF.Name != wf.Name || len(gotWF.Nodes) != 3 || len(gotWF.Edges) != 2 {
		t.Errorf("imported workflow does not match: name=%q nodes=%d edges=%d",
			gotWF.Name, len(gotWF.Nodes), len(gotWF.Edges))
	}
	if gotWF.DeadLetterSinkID != "snk-dlq" {
		t.Errorf("imported workflow lost its dead-letter sink: %q", gotWF.DeadLetterSinkID)
	}

	// Every reference the workflow makes must resolve on the target. This is the
	// assertion the old export failed: the db_lookup node's sourceId was never
	// collected, so src-lookup did not travel with the bundle.
	for _, ref := range []struct {
		kind, id, password string
	}{
		{"source", "src-main", "s3cret-orders"},
		{"source", "src-lookup", "s3cret-crm"},
	} {
		got, err := target.GetSource(ctx, ref.id)
		if err != nil {
			t.Errorf("%s %s did not travel with the bundle: %v — the imported workflow cannot start",
				ref.kind, ref.id, err)
			continue
		}
		if got.Config["db_password"] != ref.password {
			t.Errorf("%s %s lost its credential across the round trip: db_password = %q, want %q",
				ref.kind, ref.id, got.Config["db_password"], ref.password)
		}
	}
	for _, id := range []string{"snk-out", "snk-dlq"} {
		got, err := target.GetSink(ctx, id)
		if err != nil {
			t.Errorf("sink %s did not travel with the bundle: %v", id, err)
			continue
		}
		if got.Config["host"] == "" {
			t.Errorf("sink %s lost its configuration across the round trip: %v", id, got.Config)
		}
	}

	// The origin's CDC cursor is a position in the origin's database. It must
	// not arrive on the target, where it means something else entirely.
	if got, err := target.GetSource(ctx, "src-main"); err == nil && got.State["lsn"] != "" {
		t.Errorf("the imported source carries the origin's replication cursor: lsn = %q", got.State["lsn"])
	}

	// Re-importing the same bundle is an update, not a second create, and must
	// leave the target's own runtime state alone.
	if err := target.UpdateSourceState(ctx, "src-main", map[string]string{"lsn": "0/FF00FF"}); err != nil {
		t.Fatalf("advancing the target's cursor: %v", err)
	}
	reimportReq := httptest.NewRequestWithContext(ctx, "POST", "/api/workflows/import", bytes.NewReader(exported))
	reimportReq.Header.Set("Content-Type", "application/json")
	reimportRR := httptest.NewRecorder()
	handlerFor(target).ServeHTTP(reimportRR, reimportReq)
	if reimportRR.Code != http.StatusOK {
		t.Fatalf("re-import: HTTP %d (want 200 for an update), body %s", reimportRR.Code, reimportRR.Body.String())
	}
	got, err := target.GetSource(ctx, "src-main")
	if err != nil {
		t.Fatalf("reading src-main after re-import: %v", err)
	}
	if got.State["lsn"] != "0/FF00FF" {
		t.Errorf("re-importing the bundle rewound the target's own replication cursor: lsn = %q, want %q",
			got.State["lsn"], "0/FF00FF")
	}
	if got.Config["db_password"] != "s3cret-orders" {
		t.Errorf("re-import lost the credential: db_password = %q", got.Config["db_password"])
	}
}

// A source node keeps the row Test Connection sampled, as `lastSample`, in its
// own config: real data out of the source's database, persisted with the
// workflow. The export already drops a source record's sample
// (stripSourceRuntime), but the same row sitting in a node config went out in
// the bundle to wherever the file was sent.
func TestExportLeavesSampledRowsBehind(t *testing.T) {
	ctx := t.Context()
	store := newSQLiteStore(t, "samples")
	const row = "ada@example.com"

	if err := store.CreateSource(ctx, storage.Source{
		ID: "src-customers", Name: "customers", Type: "webhook", VHost: "default",
		Config: hermod.StringMap{"path": "/customers"},
		Sample: `{"email":"` + row + `"}`,
	}); err != nil {
		t.Fatalf("seed source: %v", err)
	}
	if err := store.CreateWorkflow(ctx, storage.Workflow{
		ID: "wf-samples", Name: "samples", VHost: "default",
		Nodes: []storage.WorkflowNode{
			{ID: "n-src", Type: "source", RefID: "src-customers", Config: map[string]any{
				"label":      "Customers",
				"lastSample": map[string]any{"email": row},
			}},
			{ID: "n-tr", Type: "transformation", Config: map[string]any{
				"transType":  "set",
				"testResult": map[string]any{"payload": map[string]any{"email": row}},
			}},
		},
		Edges: []storage.WorkflowEdge{{ID: "e1", SourceID: "n-src", TargetID: "n-tr"}},
	}); err != nil {
		t.Fatalf("seed workflow: %v", err)
	}

	req := httptest.NewRequestWithContext(ctx, "GET", "/api/workflows/wf-samples/export", nil)
	rr := httptest.NewRecorder()
	handlerFor(store).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("export: HTTP %d, body %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), row) {
		t.Errorf("the export bundle carries a row sampled from the source: %s", rr.Body.String())
	}

	// Only the captured data goes; the rest of each node's config is the export.
	var bundle storage.WorkflowExportBundle
	if err := json.Unmarshal(rr.Body.Bytes(), &bundle); err != nil {
		t.Fatalf("decode bundle: %v", err)
	}
	configs := map[string]map[string]any{}
	for _, n := range bundle.Workflow.Nodes {
		configs[n.ID] = n.Config
	}
	if configs["n-src"]["label"] != "Customers" || configs["n-tr"]["transType"] != "set" {
		t.Errorf("the export dropped node configuration along with the samples: %v", configs)
	}

	// Stripping is for the export only: the stored workflow keeps what the
	// editor reads.
	stored, err := store.GetWorkflow(ctx, "wf-samples")
	if err != nil {
		t.Fatalf("re-read workflow: %v", err)
	}
	for _, n := range stored.Nodes {
		if n.ID == "n-src" && n.Config["lastSample"] == nil {
			t.Error("exporting the workflow deleted lastSample from the stored copy")
		}
	}
}
