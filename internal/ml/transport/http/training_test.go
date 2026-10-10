package http

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// workerStub is a hermod-ml worker that answers every dataset and training
// route from memory.
type workerStub struct {
	mu       sync.Mutex
	calls    []string
	uploaded string
	versions []worker.Version
	busy     bool
}

func (s *workerStub) start(t *testing.T) *worker.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		p := r.URL.Path
		s.calls = append(s.calls, r.Method+" "+r.URL.RequestURI())
		switch {
		case p == "/v2/health/ready":
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && p == "/v1/datasets/tenant-a":
			_, _ = io.WriteString(w, `{"datasets":[{"name":"customers","rows":2,"columns":[{"name":"age","type":"number"}],"updated_at":"2026-10-10T00:00:00Z"}]}`)
		case r.Method == http.MethodGet && p == "/v1/datasets/tenant-a/customers":
			_, _ = io.WriteString(w, `{"name":"customers","rows":2,"columns":[],"updated_at":"2026-10-10T00:00:00Z","sample":[{"age":30}]}`)
		case r.Method == http.MethodPut && p == "/v1/datasets/tenant-a/customers/file":
			b, _ := io.ReadAll(r.Body)
			s.uploaded = string(b)
			_, _ = io.WriteString(w, `{"name":"customers","rows":2,"columns":[],"updated_at":"2026-10-10T00:00:00Z"}`)
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(p, "/train"):
			if s.busy {
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = io.WriteString(w, `{"error":"a training is already running"}`)
				return
			}
			v := worker.Version{Model: "churn", Version: "1", Task: "classification", Dataset: "customers",
				Target: "churned", Features: []string{"age"}, Metrics: map[string]float64{"score": 0.6}}
			s.versions = append([]worker.Version{v}, s.versions...)
			_ = json.NewEncoder(w).Encode(v)
		case strings.HasSuffix(p, "/versions"):
			_ = json.NewEncoder(w).Encode(map[string]any{"versions": s.versions})
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":"no such thing"}`)
		}
	}))
	t.Cleanup(srv.Close)
	return worker.New(srv.URL, "", nil)
}

func newTrainingAPI(t *testing.T) (*Handler, *modelAPIStore, *workerStub) {
	t.Helper()
	h, store := newModelAPI()
	stub := &workerStub{}
	h.worker = stub.start(t)
	return h, store, stub
}

// do runs one handler with arbitrary path values and query.
func do(h http.HandlerFunc, user *storage.User, method, target string, body io.Reader, values map[string]string) *httptest.ResponseRecorder {
	ctx := context.Background()
	if user != nil {
		ctx = context.WithValue(ctx, handlers.UserContextKey, user)
	}
	r := httptest.NewRequestWithContext(ctx, method, target, body)
	for k, v := range values {
		r.SetPathValue(k, v)
	}
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

func TestWorkerStatusSaysWhetherTrainingIsAvailable(t *testing.T) {
	h, _, _ := newTrainingAPI(t)
	w := do(h.WorkerStatus, viewerA, http.MethodGet, "/api/ml/worker", nil, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"configured":true`) || !strings.Contains(w.Body.String(), `"ready":true`) {
		t.Errorf("status = %d %s", w.Code, w.Body)
	}

	h.worker = nil
	h.noEnvWorker = true
	w = do(h.WorkerStatus, viewerA, http.MethodGet, "/api/ml/worker", nil, nil)
	if !strings.Contains(w.Body.String(), `"configured":false`) {
		t.Errorf("no worker: %s", w.Body)
	}
}

func TestDatasetsListShowUploadAndDelete(t *testing.T) {
	h, _, stub := newTrainingAPI(t)
	vals := map[string]string{"vhost": "tenant-a", "name": "customers"}

	w := do(h.ListDatasets, viewerA, http.MethodGet, "/api/vhosts/tenant-a/ml/datasets", nil, vals)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"customers"`) {
		t.Errorf("list = %d %s", w.Code, w.Body)
	}
	if w := do(h.GetDataset, viewerA, http.MethodGet, "/x", nil, vals); w.Code != http.StatusForbidden {
		t.Errorf("a viewer read a dataset's rows: %d", w.Code)
	}
	if w := do(h.GetDataset, editorA, http.MethodGet, "/x", nil, vals); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "sample") {
		t.Errorf("get = %d %s", w.Code, w.Body)
	}

	w = do(h.UploadDataset, editorA, http.MethodPut, "/x?format=csv", strings.NewReader("age,churned\n30,no\n"), vals)
	if w.Code != http.StatusOK || stub.uploaded != "age,churned\n30,no\n" {
		t.Errorf("upload = %d %s, worker got %q", w.Code, w.Body, stub.uploaded)
	}
	if w := do(h.UploadDataset, editorA, http.MethodPut, "/x?format=xls", strings.NewReader("x"), vals); w.Code != http.StatusBadRequest {
		t.Errorf("an .xls upload: %d", w.Code)
	}
	if w := do(h.UploadDataset, editorB, http.MethodPut, "/x?format=csv", strings.NewReader("x"), vals); w.Code != http.StatusForbidden {
		t.Errorf("another vhost's editor uploaded: %d", w.Code)
	}

	if w := do(h.DeleteDataset, editorA, http.MethodDelete, "/x", nil, vals); w.Code != http.StatusNoContent {
		t.Errorf("delete = %d %s", w.Code, w.Body)
	}
}

func TestDatasetFromQueryNeedsTheEngineAndASource(t *testing.T) {
	h, _, _ := newTrainingAPI(t)
	vals := map[string]string{"vhost": "tenant-a", "name": "customers"}
	if w := do(h.DatasetFromQuery, editorA, http.MethodPost, "/x", strings.NewReader(`{"query":"SELECT 1"}`), vals); w.Code != http.StatusBadRequest {
		t.Errorf("no source: %d %s", w.Code, w.Body)
	}
	w := do(h.DatasetFromQuery, editorA, http.MethodPost, "/x", strings.NewReader(`{"source_id":"s","query":"SELECT 1"}`), vals)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("without the engine: %d %s", w.Code, w.Body)
	}
}

func TestTrainVersionsAndPromoteOverTheAPI(t *testing.T) {
	h, store, stub := newTrainingAPI(t)
	vals := map[string]string{"vhost": "tenant-a", "name": "churn"}

	body := `{"dataset":"customers","target":"churned","go_live":{"mode":"if","min":0.8}}`
	w := do(h.TrainModel, editorA, http.MethodPost, "/x", strings.NewReader(body), vals)
	if w.Code != http.StatusOK {
		t.Fatalf("train = %d %s", w.Code, w.Body)
	}
	var res struct {
		Live    bool   `json:"live"`
		Reason  string `json:"reason"`
		Version struct {
			Version string `json:"version"`
		} `json:"version"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	if res.Live || res.Version.Version != "1" || !strings.Contains(res.Reason, "below") {
		t.Errorf("result = %+v, want version 1 kept off below the bar", res)
	}
	if m := store.models["tenant-a/churn"]; m.Backend != storage.MLBackendWorker || m.RemoteVersion != "" || m.UpdatedBy != "ada" {
		t.Errorf("registered = %+v", m)
	}
	if !hasAudit(store, `"trained_version":"1"`) {
		t.Error("training was not audited")
	}

	w = do(h.ListVersions, viewerA, http.MethodGet, "/x", nil, vals)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"version":"1"`) {
		t.Errorf("versions = %d %s", w.Code, w.Body)
	}

	pv := map[string]string{"vhost": "tenant-a", "name": "churn", "version": "1"}
	if w := do(h.PromoteVersion, editorA, http.MethodPost, "/x", nil, pv); w.Code != http.StatusOK {
		t.Fatalf("promote = %d %s", w.Code, w.Body)
	}
	if store.models["tenant-a/churn"].RemoteVersion != "1" {
		t.Error("version 1 is not live after promotion")
	}
	pv["version"] = "7"
	if w := do(h.PromoteVersion, editorA, http.MethodPost, "/x", nil, pv); w.Code != http.StatusNotFound {
		t.Errorf("promoting a missing version: %d", w.Code)
	}

	stub.busy = true
	if w := do(h.TrainModel, editorA, http.MethodPost, "/x", strings.NewReader(body), vals); w.Code != http.StatusTooManyRequests {
		t.Errorf("a busy worker: %d %s", w.Code, w.Body)
	}
	if w := do(h.TrainModel, viewerA, http.MethodPost, "/x", strings.NewReader(body), vals); w.Code != http.StatusForbidden {
		t.Errorf("a viewer trained: %d", w.Code)
	}
	bad := `{"dataset":"customers","target":"churned","go_live":{"mode":"if"}}`
	if w := do(h.TrainModel, editorA, http.MethodPost, "/x", strings.NewReader(bad), vals); w.Code != http.StatusBadRequest {
		t.Errorf("an 'if' rule with no bound: %d %s", w.Code, w.Body)
	}
}

func TestTrainingWithoutAWorkerIsUnavailable(t *testing.T) {
	h, _, _ := newTrainingAPI(t)
	h.worker, h.noEnvWorker = nil, true
	w := do(h.TrainModel, editorA, http.MethodPost, "/x", bytes.NewBufferString(`{"dataset":"d","target":"y"}`),
		map[string]string{"vhost": "tenant-a", "name": "churn"})
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "HERMOD_ML_WORKER_URL") {
		t.Errorf("train without a worker = %d %s", w.Code, w.Body)
	}
}

func hasAudit(s *modelAPIStore, fragment string) bool {
	for _, a := range s.audits {
		if strings.Contains(a.Payload, fragment) {
			return true
		}
	}
	return false
}
