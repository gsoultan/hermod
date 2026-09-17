package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/internal/testutil"
)

// bundleStore is a storage double that records every write the import handler
// makes, so a test can assert on what was *persisted* rather than only on the
// HTTP status.
type bundleStore struct {
	testutil.BaseMockStorage

	workflows map[string]storage.Workflow
	sources   map[string]storage.Source
	sinks     map[string]storage.Sink

	// Injected failures.
	getSourceErr  map[string]error
	getSinkErr    map[string]error
	createSrcErr  error
	updateSrcErr  error
	createSinkErr error
	updateSinkErr error
}

func newBundleStore() *bundleStore {
	return &bundleStore{
		workflows:    map[string]storage.Workflow{},
		sources:      map[string]storage.Source{},
		sinks:        map[string]storage.Sink{},
		getSourceErr: map[string]error{},
		getSinkErr:   map[string]error{},
	}
}

func (m *bundleStore) GetWorkflow(ctx context.Context, id string) (storage.Workflow, error) {
	wf, ok := m.workflows[id]
	if !ok {
		return storage.Workflow{}, storage.ErrNotFound
	}
	return wf, nil
}

func (m *bundleStore) CreateWorkflow(ctx context.Context, wf storage.Workflow) error {
	m.workflows[wf.ID] = wf
	return nil
}

func (m *bundleStore) UpdateWorkflow(ctx context.Context, wf storage.Workflow) error {
	m.workflows[wf.ID] = wf
	return nil
}

func (m *bundleStore) GetSource(ctx context.Context, id string) (storage.Source, error) {
	if err, ok := m.getSourceErr[id]; ok {
		return storage.Source{}, err
	}
	src, ok := m.sources[id]
	if !ok {
		return storage.Source{}, storage.ErrNotFound
	}
	return src, nil
}

func (m *bundleStore) GetSink(ctx context.Context, id string) (storage.Sink, error) {
	if err, ok := m.getSinkErr[id]; ok {
		return storage.Sink{}, err
	}
	snk, ok := m.sinks[id]
	if !ok {
		return storage.Sink{}, storage.ErrNotFound
	}
	return snk, nil
}

func (m *bundleStore) CreateSource(ctx context.Context, src storage.Source) error {
	if m.createSrcErr != nil {
		return m.createSrcErr
	}
	m.sources[src.ID] = src
	return nil
}

func (m *bundleStore) UpdateSource(ctx context.Context, src storage.Source) error {
	if m.updateSrcErr != nil {
		return m.updateSrcErr
	}
	m.sources[src.ID] = src
	return nil
}

func (m *bundleStore) CreateSink(ctx context.Context, snk storage.Sink) error {
	if m.createSinkErr != nil {
		return m.createSinkErr
	}
	m.sinks[snk.ID] = snk
	return nil
}

func (m *bundleStore) UpdateSink(ctx context.Context, snk storage.Sink) error {
	if m.updateSinkErr != nil {
		return m.updateSinkErr
	}
	m.sinks[snk.ID] = snk
	return nil
}

func exportHandler(store storage.Storage) *http.ServeMux {
	h := &WorkflowHandler{Handler: &handlers.Handler{Storage: store, LogStorage: store}}
	mux := http.NewServeMux()
	h.RegisterWorkflowRoutes(mux)
	return mux
}

func exportBundle(t *testing.T, mux *http.ServeMux, id string) (storage.WorkflowExportBundle, *httptest.ResponseRecorder) {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), "GET", "/api/workflows/"+id+"/export", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	var bundle storage.WorkflowExportBundle
	if rr.Code == http.StatusOK {
		if err := json.Unmarshal(rr.Body.Bytes(), &bundle); err != nil {
			t.Fatalf("decoding export bundle: %v (body %s)", err, rr.Body.String())
		}
	}
	return bundle, rr
}

func hasSource(b storage.WorkflowExportBundle, id string) bool {
	for _, s := range b.Sources {
		if s.ID == id {
			return true
		}
	}
	return false
}

func hasSink(b storage.WorkflowExportBundle, id string) bool {
	for _, s := range b.Sinks {
		if s.ID == id {
			return true
		}
	}
	return false
}

// TestExportBundlesSourcesReferencedByNodeConfig covers the references the
// export's node scan never looked at.
//
// ExportWorkflow collected dependencies by walking nodes and taking RefID from
// the ones typed "source" or "sink". But a source is also reachable through a
// *transformation node's config*: db_lookup and the enrichment SQL node both
// name one under "sourceId" (SQLConfig.tsx also writes the "sourceID" spelling).
// Those sources were left out of the bundle, so the import produced a workflow
// that starts and then fails every message with
// "failed to get source for lookup (sourceId: ...)".
func TestExportBundlesSourcesReferencedByNodeConfig(t *testing.T) {
	store := newBundleStore()
	store.sources["src-main"] = storage.Source{ID: "src-main", Name: "Main"}
	store.sources["src-lookup"] = storage.Source{ID: "src-lookup", Name: "Lookup"}
	store.sources["src-sql"] = storage.Source{ID: "src-sql", Name: "Enrichment SQL"}
	store.sinks["snk-1"] = storage.Sink{ID: "snk-1", Name: "Out"}
	store.workflows["wf-1"] = storage.Workflow{
		ID:   "wf-1",
		Name: "Enriched",
		Nodes: []storage.WorkflowNode{
			{ID: "n1", Type: "source", RefID: "src-main"},
			{ID: "n2", Type: "transformation", Config: map[string]any{
				"transType": "db_lookup",
				"sourceId":  "src-lookup",
			}},
			{ID: "n3", Type: "transformation", Config: map[string]any{
				"transType": "execute_sql",
				"sourceID":  "src-sql",
			}},
			{ID: "n4", Type: "sink", RefID: "snk-1"},
		},
	}

	mux := exportHandler(store)
	bundle, rr := exportBundle(t, mux, "wf-1")
	if rr.Code != http.StatusOK {
		t.Fatalf("export returned HTTP %d: %s", rr.Code, rr.Body.String())
	}

	for _, id := range []string{"src-main", "src-lookup", "src-sql"} {
		if !hasSource(bundle, id) {
			t.Errorf("export bundle is missing source %q; importing it elsewhere yields a workflow "+
				"whose lookup node points at a source that does not exist (bundle has %d sources)",
				id, len(bundle.Sources))
		}
	}
}

// TestExportBundlesBatchSQLUnderlyingSource covers the one transitive reference
// a source can hold: a "batch_sql" source delegates its connection to another
// source named by config["source_id"] (registry.go resolves it at pool-open
// time). Exporting only the batch_sql source produces a bundle that cannot
// connect anywhere on the importing instance.
func TestExportBundlesBatchSQLUnderlyingSource(t *testing.T) {
	store := newBundleStore()
	store.sources["src-batch"] = storage.Source{
		ID:     "src-batch",
		Name:   "Nightly batch",
		Type:   "batch_sql",
		Config: hermod.StringMap{"source_id": "src-db", "cron": "0 2 * * *"},
	}
	store.sources["src-db"] = storage.Source{ID: "src-db", Name: "Warehouse", Type: "postgres"}
	store.workflows["wf-batch"] = storage.Workflow{
		ID:    "wf-batch",
		Name:  "Batch",
		Nodes: []storage.WorkflowNode{{ID: "n1", Type: "source", RefID: "src-batch"}},
	}

	mux := exportHandler(store)
	bundle, rr := exportBundle(t, mux, "wf-batch")
	if rr.Code != http.StatusOK {
		t.Fatalf("export returned HTTP %d: %s", rr.Code, rr.Body.String())
	}

	if !hasSource(bundle, "src-db") {
		t.Errorf("export bundle omits the source a batch_sql source delegates to (source_id=src-db); "+
			"the imported batch_sql source has no connection details to fall back on (bundle has %d sources)",
			len(bundle.Sources))
	}
}

// TestExportReportsMissingReferences: a referenced source or sink that is not in
// storage was dropped from the bundle without a word, so the export looked
// complete and the import quietly produced a broken workflow. The bundle has to
// name what it could not include.
func TestExportReportsMissingReferences(t *testing.T) {
	store := newBundleStore()
	store.sinks["snk-1"] = storage.Sink{ID: "snk-1", Name: "Out"}
	store.workflows["wf-gap"] = storage.Workflow{
		ID:   "wf-gap",
		Name: "Has a dangling reference",
		Nodes: []storage.WorkflowNode{
			{ID: "n1", Type: "source", RefID: "src-deleted"},
			{ID: "n2", Type: "sink", RefID: "snk-1"},
		},
	}

	mux := exportHandler(store)
	bundle, rr := exportBundle(t, mux, "wf-gap")
	if rr.Code != http.StatusOK {
		t.Fatalf("export returned HTTP %d: %s", rr.Code, rr.Body.String())
	}

	found := false
	for _, ref := range bundle.MissingRefs {
		if ref.ID == "src-deleted" {
			found = true
		}
	}
	if !found {
		t.Errorf("export dropped the unresolvable source src-deleted silently; "+
			"MissingRefs = %+v", bundle.MissingRefs)
	}
}

// TestExportFailsWhenDependencyLookupErrors: a storage failure is not a missing
// reference. If the database hiccups while collecting dependencies, the handler
// used to treat it exactly like "not found" and hand back a bundle that is
// missing a source the instance actually has.
func TestExportFailsWhenDependencyLookupErrors(t *testing.T) {
	store := newBundleStore()
	store.sources["src-1"] = storage.Source{ID: "src-1"}
	store.getSourceErr["src-1"] = errors.New("connection reset by peer")
	store.workflows["wf-err"] = storage.Workflow{
		ID:    "wf-err",
		Name:  "Flaky",
		Nodes: []storage.WorkflowNode{{ID: "n1", Type: "source", RefID: "src-1"}},
	}

	mux := exportHandler(store)
	_, rr := exportBundle(t, mux, "wf-err")

	if rr.Code < 500 {
		t.Errorf("export returned HTTP %d when storage failed to read a referenced source; "+
			"the caller receives a bundle that silently lost a dependency (body %s)",
			rr.Code, rr.Body.String())
	}
}

// TestImportReportsDependencyStorageFailure is the sources-and-sinks half of the
// bug already fixed for the workflow itself: the upserts ran as
// `_ = h.Storage.CreateSource(...)`, so an import whose dependencies all failed
// to save still answered 201 Created. The workflow lands referencing sources
// that are not there.
func TestImportReportsDependencyStorageFailure(t *testing.T) {
	bundle := storage.WorkflowExportBundle{
		Workflow: storage.Workflow{ID: "wf-1", Name: "Imported"},
		Sources:  []storage.Source{{ID: "src-1", Name: "Main"}},
		Sinks:    []storage.Sink{{ID: "snk-1", Name: "Out"}},
	}
	body, err := json.Marshal(bundle)
	if err != nil {
		t.Fatalf("marshalling bundle: %v", err)
	}

	for _, tc := range []struct {
		name  string
		setup func(*bundleStore)
	}{
		{"source create fails", func(s *bundleStore) { s.createSrcErr = errors.New("insert violates a constraint") }},
		{"sink create fails", func(s *bundleStore) { s.createSinkErr = errors.New("disk is full") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newBundleStore()
			tc.setup(store)
			mux := exportHandler(store)

			req := httptest.NewRequestWithContext(t.Context(), "POST", "/api/workflows/import", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)

			if rr.Code < 400 {
				t.Errorf("import returned HTTP %d after a dependency failed to save; "+
					"the caller is told the import succeeded and the workflow references a "+
					"source or sink that was never written (body %s)", rr.Code, rr.Body.String())
			}
			if _, ok := store.workflows["wf-1"]; ok {
				t.Error("the workflow was written even though one of its dependencies could not be saved")
			}
		})
	}
}

// TestImportChecksVHostOfBundledResources: the permission check only looked at
// the workflow's vhost. The sources and sinks in a bundle carry their own, and
// the import upserts them by ID — so an editor confined to one vhost could hand
// in a bundle that overwrites a source belonging to a vhost they cannot read.
func TestImportChecksVHostOfBundledResources(t *testing.T) {
	store := newBundleStore()
	store.sources["src-prod"] = storage.Source{
		ID: "src-prod", Name: "Production DB", VHost: "prod",
		Config: hermod.StringMap{"host": "prod.internal"},
	}
	mux := exportHandler(store)

	bundle := storage.WorkflowExportBundle{
		Workflow: storage.Workflow{ID: "wf-1", Name: "Trojan", VHost: "team-a"},
		Sources: []storage.Source{{
			ID: "src-prod", Name: "Attacker DB", VHost: "prod",
			Config: hermod.StringMap{"host": "attacker.example.com"},
		}},
	}
	body, err := json.Marshal(bundle)
	if err != nil {
		t.Fatalf("marshalling bundle: %v", err)
	}

	ctx := context.WithValue(t.Context(), handlers.UserContextKey,
		&storage.User{Role: storage.RoleEditor, VHosts: []string{"team-a"}})
	req := httptest.NewRequestWithContext(ctx, "POST", "/api/workflows/import", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("import returned HTTP %d for an editor with access to %v importing a source in vhost %q; want 403",
			rr.Code, []string{"team-a"}, "prod")
	}
	if got := store.sources["src-prod"].Config["host"]; got != "prod.internal" {
		t.Errorf("the production source was overwritten by the import: host = %q, want %q", got, "prod.internal")
	}
}

// TestImportDoesNotCarryRuntimeState: a bundle is a description of a workflow,
// not of a running one. The exported JSON carries the *source instance's*
// runtime columns — a source's CDC cursor in State, the worker that happened to
// own it, the live counters — and the import wrote them straight through.
// Re-importing over an existing source therefore rewound (or fast-forwarded) a
// live replication cursor, which is silent data loss.
func TestImportDoesNotCarryRuntimeState(t *testing.T) {
	store := newBundleStore()
	store.sources["src-1"] = storage.Source{
		ID: "src-1", Name: "Main", Status: "running", WorkerID: "worker-here",
		State: map[string]string{"lsn": "0/4A2F1B8"},
	}
	store.sinks["snk-1"] = storage.Sink{ID: "snk-1", Name: "Out", Status: "running", WorkerID: "worker-here"}
	store.workflows["wf-1"] = storage.Workflow{
		ID: "wf-1", Name: "Existing", Status: "running", WorkerID: "worker-here",
		TotalProcessed: 9_000, TotalErrors: 3,
	}
	mux := exportHandler(store)

	bundle := storage.WorkflowExportBundle{
		Workflow: storage.Workflow{
			ID: "wf-1", Name: "Imported", Status: "error", WorkerID: "worker-elsewhere",
			TotalProcessed: 1, TotalErrors: 999, TotalLag: 42,
		},
		Sources: []storage.Source{{
			ID: "src-1", Name: "Main", Status: "error", WorkerID: "worker-elsewhere",
			State: map[string]string{"lsn": "0/0000001"},
		}},
		Sinks: []storage.Sink{{ID: "snk-1", Name: "Out", Status: "error", WorkerID: "worker-elsewhere"}},
	}
	body, err := json.Marshal(bundle)
	if err != nil {
		t.Fatalf("marshalling bundle: %v", err)
	}

	req := httptest.NewRequestWithContext(t.Context(), "POST", "/api/workflows/import", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code >= 400 {
		t.Fatalf("import failed: HTTP %d, body %s", rr.Code, rr.Body.String())
	}

	gotSrc := store.sources["src-1"]
	if gotSrc.State["lsn"] != "0/4A2F1B8" {
		t.Errorf("import overwrote the live CDC cursor of source src-1: lsn = %q, want %q — "+
			"the source will re-read or skip everything between the two positions",
			gotSrc.State["lsn"], "0/4A2F1B8")
	}
	if gotSrc.WorkerID != "worker-here" || gotSrc.Status == "error" {
		t.Errorf("import carried the exporting instance's runtime fields onto source src-1: "+
			"worker_id = %q, status = %q", gotSrc.WorkerID, gotSrc.Status)
	}
	if gotSrc.Name != "Main" {
		t.Errorf("import did not apply the bundle's configuration: source name = %q, want %q", gotSrc.Name, "Main")
	}

	gotSnk := store.sinks["snk-1"]
	if gotSnk.WorkerID != "worker-here" || gotSnk.Status == "error" {
		t.Errorf("import carried the exporting instance's runtime fields onto sink snk-1: "+
			"worker_id = %q, status = %q", gotSnk.WorkerID, gotSnk.Status)
	}

	gotWF := store.workflows["wf-1"]
	if gotWF.WorkerID != "worker-here" {
		t.Errorf("import reassigned the workflow to a worker from another instance: worker_id = %q, want %q",
			gotWF.WorkerID, "worker-here")
	}
	if gotWF.TotalProcessed != 9_000 || gotWF.TotalErrors != 3 || gotWF.TotalLag != 0 {
		t.Errorf("import replaced this instance's counters with the bundle's: processed=%d errors=%d lag=%d; want 9000/3/0",
			gotWF.TotalProcessed, gotWF.TotalErrors, gotWF.TotalLag)
	}
	if gotWF.Name != "Imported" {
		t.Errorf("import did not apply the bundle's configuration: workflow name = %q, want %q", gotWF.Name, "Imported")
	}
	if !hasSink(storage.WorkflowExportBundle{Sinks: []storage.Sink{gotSnk}}, "snk-1") {
		t.Error("sink snk-1 vanished from storage during the import")
	}
}

// TestExportOmitsRuntimeState is the other half of TestImportDoesNotCarryRuntimeState.
// The import defends itself because a bundle is user-supplied JSON, but the file
// the export writes should not contain this instance's CDC cursor, worker
// assignment, live counters or sampled rows in the first place — it travels to
// other machines and describes a workflow, not a run of one.
func TestExportOmitsRuntimeState(t *testing.T) {
	leaseUntil := time.Now().Add(time.Hour)
	store := newBundleStore()
	store.sources["src-1"] = storage.Source{
		ID: "src-1", Name: "Main", Active: true, Status: "running", WorkerID: "worker-1",
		State: map[string]string{"lsn": "0/4A2F1B8"}, Sample: `{"ssn":"123-45-6789"}`,
	}
	store.sinks["snk-1"] = storage.Sink{ID: "snk-1", Name: "Out", Active: true, Status: "running", WorkerID: "worker-1"}
	store.workflows["wf-1"] = storage.Workflow{
		ID: "wf-1", Name: "Live", Active: true, Status: "running",
		WorkerID: "worker-1", OwnerID: "worker-1", LeaseUntil: &leaseUntil,
		TotalProcessed: 9_000, TotalErrors: 3, TotalLag: 12,
		Nodes: []storage.WorkflowNode{
			{ID: "n1", Type: "source", RefID: "src-1"},
			{ID: "n2", Type: "sink", RefID: "snk-1"},
		},
	}

	mux := exportHandler(store)
	bundle, rr := exportBundle(t, mux, "wf-1")
	if rr.Code != http.StatusOK {
		t.Fatalf("export returned HTTP %d: %s", rr.Code, rr.Body.String())
	}

	wf := bundle.Workflow
	if wf.Status != "" || wf.WorkerID != "" || wf.OwnerID != "" || wf.LeaseUntil != nil {
		t.Errorf("exported workflow carries runtime fields: status=%q worker_id=%q owner_id=%q lease_until=%v",
			wf.Status, wf.WorkerID, wf.OwnerID, wf.LeaseUntil)
	}
	if wf.TotalProcessed != 0 || wf.TotalErrors != 0 || wf.TotalLag != 0 {
		t.Errorf("exported workflow carries this instance's counters: processed=%d errors=%d lag=%d",
			wf.TotalProcessed, wf.TotalErrors, wf.TotalLag)
	}
	if !wf.Active {
		t.Error("export cleared Active, which is configuration (should this workflow run), not runtime")
	}

	if len(bundle.Sources) != 1 {
		t.Fatalf("expected 1 source in the bundle, got %d", len(bundle.Sources))
	}
	src := bundle.Sources[0]
	if src.Status != "" || src.WorkerID != "" || src.State != nil || src.Sample != "" {
		t.Errorf("exported source carries runtime fields: status=%q worker_id=%q state=%v sample=%q",
			src.Status, src.WorkerID, src.State, src.Sample)
	}
	if !src.Active || src.Name != "Main" {
		t.Errorf("export dropped source configuration: active=%v name=%q", src.Active, src.Name)
	}

	if len(bundle.Sinks) != 1 {
		t.Fatalf("expected 1 sink in the bundle, got %d", len(bundle.Sinks))
	}
	if snk := bundle.Sinks[0]; snk.Status != "" || snk.WorkerID != "" {
		t.Errorf("exported sink carries runtime fields: status=%q worker_id=%q", snk.Status, snk.WorkerID)
	}
}

// TestExportFilenameIsSafe: the Content-Disposition header interpolated the
// workflow name raw, so a name with a quote in it produced a filename the
// browser reads as ending early, and a name with a slash proposed a path.
func TestExportFilenameIsSafe(t *testing.T) {
	store := newBundleStore()
	store.workflows["wf-1"] = storage.Workflow{ID: "wf-1", Name: `../../etc/"passwd`}
	mux := exportHandler(store)

	_, rr := exportBundle(t, mux, "wf-1")
	if rr.Code != http.StatusOK {
		t.Fatalf("export returned HTTP %d: %s", rr.Code, rr.Body.String())
	}

	cd := rr.Header().Get("Content-Disposition")
	if strings.ContainsAny(strings.TrimPrefix(cd, `attachment; filename=`), `/\`) {
		t.Errorf("Content-Disposition proposes a path: %q", cd)
	}
	if strings.Count(cd, `"`) != 2 {
		t.Errorf("Content-Disposition filename is not a single quoted token: %q", cd)
	}
}
