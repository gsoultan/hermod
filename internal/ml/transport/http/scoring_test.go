package http

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/ml"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/inference"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// modelAPIStore keeps the scoring mode as the real stores do.
func (s *modelAPIStore) SetMLModelScoring(_ context.Context, vhost, name, scoring string) error {
	if err := storage.ValidateMLScoring(scoring); err != nil {
		return err
	}
	m, ok := s.models[vhost+"/"+name]
	if !ok {
		return storage.ErrNotFound
	}
	m.Scoring = scoring
	s.models[vhost+"/"+name] = m
	return nil
}

// fixture loads one of pkg/ml/onnxscore's parity fixtures into the stub
// worker as version 1 of its model.
func (s *workerStub) fixture(t *testing.T, name string) {
	t.Helper()
	dir := filepath.Join("..", "..", "..", "..", "pkg", "ml", "onnxscore", "testdata", "trained", name)
	raw, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v worker.Version
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	if s.onnx, err = os.ReadFile(filepath.Join(dir, "model.onnx")); err != nil {
		t.Fatal(err)
	}
	s.versions = []worker.Version{v}
}

func TestScoringIsSetAndReportedOverTheAPI(t *testing.T) {
	h, store, stub := newTrainingAPI(t)
	stub.fixture(t, "gradient_boosting_regression")
	store.models["tenant-a/churn"] = storage.MLModel{VHost: "tenant-a", Name: "churn", Backend: storage.MLBackendWorker, RemoteVersion: "1"}
	store.models["tenant-a/fraud"] = storage.MLModel{VHost: "tenant-a", Name: "fraud", Backend: inference.BackendOIP, URL: "http://ml:8080", RemoteModel: "fraud"}
	vals := map[string]string{"vhost": "tenant-a", "name": "churn"}

	w := do(h.GetScoring, viewerA, http.MethodGet, "/x", nil, vals)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"scoring":"worker"`) || !strings.Contains(w.Body.String(), `"in_process":false`) {
		t.Fatalf("before = %d %s", w.Code, w.Body)
	}

	w = do(h.PutScoring, editorA, http.MethodPut, "/x", strings.NewReader(`{"scoring":"in_process"}`), vals)
	if w.Code != http.StatusOK {
		t.Fatalf("set = %d %s", w.Code, w.Body)
	}
	var status ml.ScoringStatus
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.Scoring != storage.MLScoringInProcess || !status.InProcess || status.Version != "1" || len(status.Ops) == 0 {
		t.Errorf("status = %+v, want in-process scoring of version 1 in use", status)
	}
	if store.models["tenant-a/churn"].Scoring != storage.MLScoringInProcess {
		t.Errorf("stored scoring = %q", store.models["tenant-a/churn"].Scoring)
	}
	if !hasAudit(store, `"scoring":"in_process"`) {
		t.Error("changing how a model is scored was not audited")
	}

	// A prediction from the UI is now scored in Hermod: the stub worker has
	// no infer route, so an answer can only have come from the graph.
	w = do(h.Predict, editorA, http.MethodPost, "/x",
		strings.NewReader(`{"instances":[{"x1":1.5,"x2":3,"city":"Oslo","flag":true}]}`), vals)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"value":`) {
		t.Errorf("predict = %d %s", w.Code, w.Body)
	}

	for _, tc := range []struct {
		name string
		user *storage.User
		vals map[string]string
		body string
		want int
	}{
		{"a viewer", viewerA, vals, `{"scoring":"worker"}`, http.StatusForbidden},
		{"another vhost's editor", editorB, vals, `{"scoring":"worker"}`, http.StatusForbidden},
		{"a model served elsewhere", editorA, map[string]string{"vhost": "tenant-a", "name": "fraud"}, `{"scoring":"in_process"}`, http.StatusBadRequest},
		{"a model that does not exist", editorA, map[string]string{"vhost": "tenant-a", "name": "nope"}, `{"scoring":"worker"}`, http.StatusNotFound},
		{"an unknown mode", editorA, vals, `{"scoring":"gpu"}`, http.StatusBadRequest},
		{"not JSON", editorA, vals, `{`, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if w := do(h.PutScoring, tc.user, http.MethodPut, "/x", strings.NewReader(tc.body), tc.vals); w.Code != tc.want {
				t.Errorf("status = %d %s, want %d", w.Code, w.Body, tc.want)
			}
		})
	}
	if w := do(h.GetScoring, editorB, http.MethodGet, "/x", nil, vals); w.Code != http.StatusForbidden {
		t.Errorf("another vhost read the scoring status: %d", w.Code)
	}
}
