// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

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
	// onnx is the graph every version serves (scoring_test.go), and rec the
	// recommender artifact (recommend_test.go).
	onnx []byte
	rec  []byte
	// trained is the last training spec the worker was sent, and checked
	// the last pre-flight check's.
	trained, checked string
	// capabilities answers /v1/capabilities; empty answers 404, like a
	// worker from before that route.
	capabilities string
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
		case p == "/v1/capabilities" && s.capabilities != "":
			_, _ = io.WriteString(w, s.capabilities)
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
		case r.Method == http.MethodPost && p == "/v1/recommend-check/tenant-a":
			b, _ := io.ReadAll(r.Body)
			s.checked = string(b)
			_, _ = io.WriteString(w, `{"ok":true,"checks":[{"code":"few_customers","status":"amber","message":"300 customers","fix":"more"}],`+
				`"stats":{"customers":300,"products":200,"transactions":46021,"months_of_history":18,"tx_per_customer":153}}`)
		case r.Method == http.MethodPost && p == "/v1/train-check/tenant-a":
			b, _ := io.ReadAll(r.Body)
			s.checked = string(b)
			if strings.Contains(s.checked, `"dataset":"missing"`) {
				w.WriteHeader(http.StatusNotFound)
				_, _ = io.WriteString(w, `{"error":"Dataset 'missing' does not exist in vhost 'tenant-a'."}`)
				return
			}
			_, _ = io.WriteString(w, `{"task":"classification","target_kind":"yes_no","classes":2,"rows":3,"usable_rows":3,`+
				`"columns":[{"name":"id","type":"number","role":"id_like","note":"like an ID"}],`+
				`"warnings":[{"code":"few_rows","message":"Only 3 rows","column":"churned"}],"ok":false}`)
		case strings.HasSuffix(p, "/train"):
			if s.busy {
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = io.WriteString(w, `{"error":"a training is already running"}`)
				return
			}
			b, _ := io.ReadAll(r.Body)
			s.trained = string(b)
			v := worker.Version{Model: "churn", Version: "1", Task: "classification", Dataset: "customers",
				Target: "churned", Features: []string{"age"}, Metrics: map[string]float64{"score": 0.6}}
			var spec worker.TrainSpec
			if json.Unmarshal(b, &spec) == nil && spec.Task == worker.TaskRecommendation {
				v = worker.Version{Model: "recs", Version: "1", Task: worker.TaskRecommendation, Dataset: spec.Dataset,
					Recommend: spec.Recommend, Metrics: map[string]float64{"score": 0.2, "ndcg@10": 0.2}}
			}
			s.versions = append([]worker.Version{v}, s.versions...)
			_ = json.NewEncoder(w).Encode(v)
		case strings.HasSuffix(p, "/model.onnx") && s.onnx != nil:
			_, _ = w.Write(s.onnx)
		case strings.HasSuffix(p, "/model.rec") && s.rec != nil:
			_, _ = w.Write(s.rec)
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

func TestWorkerStatusListsTheAlgorithmsTheWorkerCanTrain(t *testing.T) {
	h, _, stub := newTrainingAPI(t)
	// A worker from before /v1/capabilities: ready, and no list, so the UI
	// offers every algorithm as it did.
	w := do(h.WorkerStatus, viewerA, http.MethodGet, "/api/ml/worker", nil, nil)
	if !strings.Contains(w.Body.String(), `"ready":true`) || strings.Contains(w.Body.String(), "algorithms") {
		t.Errorf("old worker: %s", w.Body)
	}

	stub.capabilities = `{"tasks":["auto"],"algorithms":["auto","random_forest","linear"],"unavailable":{"pytorch_mlp":"the torch package is not installed"}}`
	w = do(h.WorkerStatus, viewerA, http.MethodGet, "/api/ml/worker", nil, nil)
	var status struct {
		Ready       bool              `json:"ready"`
		Algorithms  []string          `json:"algorithms"`
		Unavailable map[string]string `json:"unavailable"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if !status.Ready || strings.Join(status.Algorithms, ",") != "auto,random_forest,linear" {
		t.Errorf("status = %+v", status)
	}
	if status.Unavailable["pytorch_mlp"] != "the torch package is not installed" {
		t.Errorf("unavailable = %v", status.Unavailable)
	}
}

func TestTrainPassesDeepLearningParamsToTheWorker(t *testing.T) {
	h, _, stub := newTrainingAPI(t)
	body := `{"dataset":"customers","target":"churned","algorithm":"keras_mlp",
		"params":{"hidden_layers":[32,16],"epochs":40,"batch_size":16,"learning_rate":0.005,"patience":4},"go_live":{"mode":"never"}}`
	w := do(h.TrainModel, editorA, http.MethodPost, "/x", strings.NewReader(body), map[string]string{"vhost": "tenant-a", "name": "churn"})
	if w.Code != http.StatusOK {
		t.Fatalf("train = %d %s", w.Code, w.Body)
	}
	want := `"params":{"hidden_layers":[32,16],"epochs":40,"batch_size":16,"learning_rate":0.005,"patience":4}`
	if !strings.Contains(stub.trained, `"algorithm":"keras_mlp"`) || !strings.Contains(stub.trained, want) {
		t.Errorf("worker got %s, want the algorithm and %s", stub.trained, want)
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

func TestTrainCheckProxiesThePreflightCheck(t *testing.T) {
	h, store, stub := newTrainingAPI(t)
	vals := map[string]string{"vhost": "tenant-a"}
	body := `{"dataset":"customers","target":"churned","preset":"quick","name":"churn","go_live":{"mode":"always"}}`
	w := do(h.TrainCheck, editorA, http.MethodPost, "/x", strings.NewReader(body), vals)
	if w.Code != http.StatusOK {
		t.Fatalf("train-check = %d %s", w.Code, w.Body)
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"task", "target_kind", "classes", "rows", "usable_rows", "columns", "warnings", "ok"} {
		if _, ok := got[key]; !ok {
			t.Errorf("answer has no %q: %s", key, w.Body)
		}
	}
	if got["ok"] != false || got["target_kind"] != "yes_no" {
		t.Errorf("answer = %s", w.Body)
	}
	if !strings.Contains(stub.checked, `"preset":"quick"`) || strings.Contains(stub.checked, "go_live") {
		t.Errorf("worker got %s", stub.checked)
	}
	for _, c := range stub.calls {
		if strings.HasSuffix(c, "/train") {
			t.Errorf("a check trained: %v", stub.calls)
		}
	}
	if len(store.models) != 0 {
		t.Errorf("a check registered a model: %v", store.models)
	}
}

func TestTrainCheckErrorsAreTrainingErrors(t *testing.T) {
	h, _, _ := newTrainingAPI(t)
	vals := map[string]string{"vhost": "tenant-a"}
	for _, tc := range []struct {
		name string
		user *storage.User
		body string
		code int
	}{
		{"a viewer", viewerA, `{"dataset":"customers","target":"churned"}`, http.StatusForbidden},
		{"not JSON", editorA, `{`, http.StatusBadRequest},
		{"no target", editorA, `{"dataset":"customers"}`, http.StatusBadRequest},
		{"a preset with an explicit algorithm", editorA, `{"dataset":"customers","target":"churned","algorithm":"linear","preset":"best"}`, http.StatusBadRequest},
		{"an unknown preset", editorA, `{"dataset":"customers","target":"churned","preset":"fastest"}`, http.StatusBadRequest},
		{"an unknown dataset", editorA, `{"dataset":"missing","target":"churned"}`, http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := do(h.TrainCheck, tc.user, http.MethodPost, "/x", strings.NewReader(tc.body), vals)
			if w.Code != tc.code {
				t.Errorf("code = %d %s, want %d", w.Code, w.Body, tc.code)
			}
		})
	}

	h.worker, h.noEnvWorker = nil, true
	w := do(h.TrainCheck, editorA, http.MethodPost, "/x", strings.NewReader(`{"dataset":"d","target":"y"}`), vals)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("without a worker = %d %s", w.Code, w.Body)
	}
}

// A recommender has no column to predict, so the check refuses it in words
// that say what to do, rather than asking for a target.
func TestTrainCheckRefusesARecommender(t *testing.T) {
	h, _, stub := newTrainingAPI(t)
	body := `{"dataset":"orders","task":"recommendation","recommend":{"user":"customer","item":"sku"}}`
	w := do(h.TrainCheck, editorA, http.MethodPost, "/x", strings.NewReader(body), map[string]string{"vhost": "tenant-a"})
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "recommendation model") ||
		!strings.Contains(w.Body.String(), "train it directly") {
		t.Errorf("code = %d %s, want 400 saying to train it directly", w.Code, w.Body)
	}
	if stub.checked != "" {
		t.Errorf("the worker was asked: %s", stub.checked)
	}
}

func TestTrainRefusesAPresetWithAnExplicitAlgorithm(t *testing.T) {
	h, _, stub := newTrainingAPI(t)
	body := `{"dataset":"customers","target":"churned","algorithm":"xgboost","preset":"best"}`
	w := do(h.TrainModel, editorA, http.MethodPost, "/x", strings.NewReader(body), map[string]string{"vhost": "tenant-a", "name": "churn"})
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "preset") {
		t.Errorf("train = %d %s", w.Code, w.Body)
	}
	if stub.trained != "" {
		t.Errorf("the worker trained: %s", stub.trained)
	}
}
