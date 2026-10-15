// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package http

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/ml"
	"github.com/gsoultan/hermod/internal/storage"
	sqlstore "github.com/gsoultan/hermod/internal/storage/sql"
)

// ---------------------------------------------------------------------------
// Asynchronous jobs over the API: any call that may take a while can be sent
// with "respond": "async" (or Prefer: respond-async) and is answered 202 with
// where to read it. Jobs are read with the same rights as the call: an Editor
// of the vhost, or the serving key of the model the job was submitted for.
// ---------------------------------------------------------------------------

// newJobsAPI is the model API over a real SQLite store holding model
// "double" of tenant-a, served by an MLflow server that doubles x.
func newJobsAPI(t *testing.T) (*Handler, storage.Storage) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "jobs.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	st := sqlstore.NewSQLStorage(db, "sqlite")
	if err := st.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := st.(storage.MLModelStore).PutMLModel(t.Context(), storage.MLModel{
		VHost: "tenant-a", Name: "double", Backend: "mlflow", URL: doubler(t)}); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(&handlers.Handler{Storage: st, LogStorage: st})
	h.noEnvWorker = true
	return h, st
}

// runJobs runs every queued job to its end, as a runner does.
func runJobs(t *testing.T, h *Handler) {
	t.Helper()
	ml.NewJobRunner(h.service, nil).Tick(t.Context())
}

type accepted struct {
	JobID     string `json:"job_id"`
	Status    string `json:"status"`
	StatusURL string `json:"status_url"`
}

func acceptedOf(t *testing.T, w *httptest.ResponseRecorder) accepted {
	t.Helper()
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d %s, want 202", w.Code, w.Body)
	}
	var a accepted
	if err := json.Unmarshal(w.Body.Bytes(), &a); err != nil || a.JobID == "" {
		t.Fatalf("body = %s, %v", w.Body, err)
	}
	if w.Header().Get("Location") != a.StatusURL {
		t.Errorf("Location = %q, want the status URL %q", w.Header().Get("Location"), a.StatusURL)
	}
	return a
}

type jobBody struct {
	storage.MLJob
	StatusURL string          `json:"status_url"`
	ResultURL string          `json:"result_url"`
	Result    json.RawMessage `json:"result"`
}

func jobOf(t *testing.T, w *httptest.ResponseRecorder) jobBody {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d %s", w.Code, w.Body)
	}
	var j jobBody
	if err := json.Unmarshal(w.Body.Bytes(), &j); err != nil {
		t.Fatal(err)
	}
	return j
}

func TestAsyncPredictAnswers202AndTheJobHoldsTheAnswer(t *testing.T) {
	h, _ := newJobsAPI(t)
	w := call(h.Predict, editorA, http.MethodPost, "tenant-a", "double", `{"instances":[{"x":2},{"x":5}],"respond":"async"}`)
	a := acceptedOf(t, w)
	if a.Status != storage.MLJobQueued || a.StatusURL != "/api/vhosts/tenant-a/ml/jobs/"+a.JobID {
		t.Fatalf("accepted = %+v", a)
	}
	if w.Header().Get("Preference-Applied") != "respond-async" {
		t.Errorf("Preference-Applied = %q", w.Header().Get("Preference-Applied"))
	}
	runJobs(t, h)

	path := map[string]string{"vhost": "tenant-a", "id": a.JobID}
	j := jobOf(t, do(h.GetJob, editorA, http.MethodGet, a.StatusURL, nil, path))
	if j.Status != storage.MLJobSucceeded || j.Progress != 1 || !strings.Contains(string(j.Result), `"prediction":10`) {
		t.Errorf("job = %+v result %s", j.MLJob, j.Result)
	}
	if j.Request != nil {
		t.Errorf("a prediction job's rows came back with it: %s", j.Request)
	}
	// The result on its own is the JSON the sync call answers, not encoded again.
	if w := do(h.GetJobResult, editorA, http.MethodGet, a.StatusURL+"/result", nil, path); w.Code != http.StatusOK ||
		!strings.Contains(w.Body.String(), `"predictions":[{"prediction":4}`) {
		t.Errorf("result = %d %s", w.Code, w.Body)
	}

	// Reading a job's answer is reading the model's output: an Editor of
	// the vhost may; a Viewer sees the job only in the list, without it.
	for _, u := range []*storage.User{editorB, viewerA} {
		if w := do(h.GetJob, u, http.MethodGet, a.StatusURL, nil, path); w.Code != http.StatusForbidden {
			t.Errorf("%s read the job: %d", u.Username, w.Code)
		}
	}
	if w := do(h.GetJob, editorA, http.MethodGet, "/", nil, map[string]string{"vhost": "tenant-a", "id": "job_nope"}); w.Code != http.StatusNotFound {
		t.Errorf("an unknown job = %d", w.Code)
	}
	w = do(h.ListJobs, viewerA, http.MethodGet, "/api/vhosts/tenant-a/ml/jobs?model=double&status=succeeded", nil, map[string]string{"vhost": "tenant-a"})
	var list struct {
		Data  []jobBody `json:"data"`
		Total int       `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || w.Code != http.StatusOK || list.Total != 1 ||
		list.Data[0].ID != a.JobID || list.Data[0].Result != nil {
		t.Errorf("list = %d %s", w.Code, w.Body)
	}
	if w := do(h.ListJobs, editorB, http.MethodGet, "/api/vhosts/tenant-a/ml/jobs", nil, map[string]string{"vhost": "tenant-a"}); w.Code != http.StatusForbidden {
		t.Errorf("another vhost's Editor listed the jobs: %d", w.Code)
	}
	if w := do(h.ListJobs, editorA, http.MethodGet, "/api/vhosts/tenant-a/ml/jobs?status=sleeping", nil, map[string]string{"vhost": "tenant-a"}); w.Code != http.StatusBadRequest {
		t.Errorf("an unknown status filter = %d", w.Code)
	}
}

func TestAsyncIsAskedForInTheBodyOrWithPrefer(t *testing.T) {
	h, _ := newJobsAPI(t)
	acceptedOf(t, call(h.Predict, editorA, http.MethodPost, "tenant-a", "double", `{"instances":[{"x":2}]}`, "Prefer", "respond-async, wait=5"))

	if w := call(h.Predict, editorA, http.MethodPost, "tenant-a", "double", `{"instances":[{"x":2}],"respond":"later"}`); w.Code != http.StatusBadRequest {
		t.Errorf(`"respond": "later" = %d`, w.Code)
	}
	if w := call(h.Predict, editorA, http.MethodPost, "tenant-a", "double",
		`{"instances":[{"x":2}],"callback_url":"https://example.com/hook"}`); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "async") {
		t.Errorf("a callback on a sync call = %d %s", w.Code, w.Body)
	}
	// "respond": "sync" in the body wins over the header.
	if w := call(h.Predict, editorA, http.MethodPost, "tenant-a", "double", `{"instances":[{"x":2}],"respond":"sync"}`, "Prefer", "respond-async"); w.Code != http.StatusOK {
		t.Errorf(`"respond": "sync" with Prefer: respond-async = %d`, w.Code)
	}
	// A job takes more rows than a call.
	rows := make([]map[string]int, ml.MaxRowsPerCall+1)
	for i := range rows {
		rows[i] = map[string]int{"x": i}
	}
	body, _ := json.Marshal(map[string]any{"instances": rows})
	if w := call(h.Predict, editorA, http.MethodPost, "tenant-a", "double", string(body)); w.Code != http.StatusBadRequest {
		t.Errorf("a sync call over the row limit = %d", w.Code)
	}
	body, _ = json.Marshal(map[string]any{"instances": rows, "respond": "async"})
	acceptedOf(t, call(h.Predict, editorA, http.MethodPost, "tenant-a", "double", string(body)))
}

func TestServingKeyJobsAreReadWithTheKey(t *testing.T) {
	h, st := newJobsAPI(t)
	if err := st.(storage.MLModelStore).PutMLModel(t.Context(), storage.MLModel{
		VHost: "tenant-a", Name: "other", Backend: "mlflow", URL: doubler(t)}); err != nil {
		t.Fatal(err)
	}
	key := rotateKey(t, h, "double")
	otherKey := rotateKey(t, h, "other")

	w := call(h.Serve, nil, http.MethodPost, "tenant-a", "double", `{"instances":[{"x":3}],"respond":"async"}`, "X-API-Key", key)
	a := acceptedOf(t, w)
	if a.StatusURL != "/api/ml/serve/tenant-a/jobs/"+a.JobID {
		t.Fatalf("status_url = %q", a.StatusURL)
	}
	if w := call(h.Serve, nil, http.MethodPost, "tenant-a", "double", `{"instances":[{"x":3}],"respond":"async"}`, "X-API-Key", "hml_wrong"); w.Code != http.StatusUnauthorized {
		t.Errorf("a wrong key queued a job: %d", w.Code)
	}
	runJobs(t, h)

	path := map[string]string{"vhost": "tenant-a", "id": a.JobID}
	read := func(key string) *httptest.ResponseRecorder {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, a.StatusURL, nil)
		for k, v := range path {
			r.SetPathValue(k, v)
		}
		if key != "" {
			r.Header.Set("Authorization", "Bearer "+key)
		}
		w := httptest.NewRecorder()
		h.ServeJob(w, r)
		return w
	}
	if j := jobOf(t, read(key)); j.Status != storage.MLJobSucceeded || !strings.Contains(string(j.Result), `"prediction":6`) {
		t.Errorf("job = %+v %s", j.MLJob, j.Result)
	}
	if w := read(""); w.Code != http.StatusUnauthorized {
		t.Errorf("no key = %d", w.Code)
	}
	// Every other refusal is the same 404: a key does not learn of jobs
	// that are not its own.
	if w := read(otherKey); w.Code != http.StatusNotFound {
		t.Errorf("another model's key = %d", w.Code)
	}
	if w := read("hml_wrong"); w.Code != http.StatusNotFound {
		t.Errorf("a wrong key = %d", w.Code)
	}

	// A job an Editor queued for the same model is not the key's to read.
	ui := acceptedOf(t, call(h.Predict, editorA, http.MethodPost, "tenant-a", "double", `{"instances":[{"x":1}],"respond":"async"}`))
	path["id"] = ui.JobID
	if w := read(key); w.Code != http.StatusNotFound {
		t.Errorf("the key read a job an Editor queued: %d", w.Code)
	}
}

// rotateKey makes a serving key for a model of tenant-a.
func rotateKey(t *testing.T, h *Handler, model string) string {
	t.Helper()
	w := call(h.RotateServingKey, editorA, http.MethodPost, "tenant-a", model, "")
	var k struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &k); err != nil || k.Key == "" {
		t.Fatalf("rotate: %d %s", w.Code, w.Body)
	}
	return k.Key
}

func TestCancelAJobOverTheAPI(t *testing.T) {
	h, _ := newJobsAPI(t)
	a := acceptedOf(t, call(h.Predict, editorA, http.MethodPost, "tenant-a", "double", `{"instances":[{"x":2}],"respond":"async"}`))
	path := map[string]string{"vhost": "tenant-a", "id": a.JobID}
	if w := do(h.CancelJob, viewerA, http.MethodPost, "/", nil, path); w.Code != http.StatusForbidden {
		t.Errorf("a Viewer cancelled the job: %d", w.Code)
	}
	j := jobOf(t, do(h.CancelJob, editorA, http.MethodPost, "/", nil, path))
	if j.Status != storage.MLJobCancelled {
		t.Errorf("job = %+v", j.MLJob)
	}
	runJobs(t, h)
	if j := jobOf(t, do(h.GetJob, editorA, http.MethodGet, "/", nil, path)); j.Status != storage.MLJobCancelled {
		t.Errorf("a cancelled job ran: %+v", j.MLJob)
	}
}

func TestAsyncTrainingAnswers202AndTrainsInTheBackground(t *testing.T) {
	h, _ := newJobsAPI(t)
	h.noEnvWorker = false
	stub := &workerStub{}
	h.worker = stub.start(t)

	w := call(h.TrainModel, editorA, http.MethodPost, "tenant-a", "churn",
		`{"dataset":"customers","target":"churned","go_live":{"mode":"always"},"respond":"async"}`)
	a := acceptedOf(t, w)
	if stub.trained != "" {
		t.Fatal("an async training was trained before the call returned")
	}
	runJobs(t, h)
	j := jobOf(t, do(h.GetJob, editorA, http.MethodGet, "/", nil, map[string]string{"vhost": "tenant-a", "id": a.JobID}))
	var res ml.TrainResult
	if err := json.Unmarshal(j.Result, &res); err != nil || j.Status != storage.MLJobSucceeded || res.Version.Version != "1" {
		t.Errorf("job = %+v result %s", j.MLJob, j.Result)
	}
	if j.Kind != storage.MLJobTrain || j.CreatedBy != "ada" || !strings.Contains(string(j.Request), `"dataset":"customers"`) {
		t.Errorf("a training job should say who trained what: %+v", j.MLJob)
	}
	if w := call(h.TrainModel, editorA, http.MethodPost, "tenant-a", "churn", `{"dataset":"customers","respond":"async"}`); w.Code != http.StatusBadRequest {
		t.Errorf("an async training without a target = %d", w.Code)
	}
}

func TestSyncCallsOverTheTimeLimitAreToldToUseAsync(t *testing.T) {
	h, st := newJobsAPI(t)
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(500 * time.Millisecond):
		case <-r.Context().Done():
			return
		}
		_, _ = w.Write([]byte(`{"predictions":[1]}`))
	}))
	t.Cleanup(slow.Close)
	if err := st.(storage.MLModelStore).PutMLModel(t.Context(), storage.MLModel{
		VHost: "tenant-a", Name: "slow", Backend: "mlflow", URL: slow.URL}); err != nil {
		t.Fatal(err)
	}
	limit := 50 * time.Millisecond
	h.syncTimeout = &limit

	w := call(h.Predict, editorA, http.MethodPost, "tenant-a", "slow", `{"instances":[{"x":1}]}`)
	if w.Code != http.StatusRequestTimeout || !strings.Contains(w.Body.String(), "respond-async") {
		t.Errorf("a slow sync call = %d %s, want 408 pointing at async", w.Code, w.Body)
	}
	acceptedOf(t, call(h.Predict, editorA, http.MethodPost, "tenant-a", "slow", `{"instances":[{"x":1}],"respond":"async"}`))
}
