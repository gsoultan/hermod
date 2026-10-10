package ml

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/inference"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// fakeWorker is a hermod-ml worker that trains instantly: every training
// makes the next version with the score it is told to report.
type fakeWorker struct {
	mu       sync.Mutex
	score    float64
	versions map[string][]worker.Version // "vhost/model", newest first
	calls    []string
	auth     string
	deleted  []string
}

func (f *fakeWorker) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		path := r.URL.EscapedPath()
		f.calls = append(f.calls, r.Method+" "+path)
		f.auth = r.Header.Get("Authorization")
		parts := strings.Split(strings.Trim(path, "/"), "/")
		switch {
		case r.Method == http.MethodPost && len(parts) == 5 && parts[0] == "v1" && parts[4] == "train":
			var spec worker.TrainSpec
			_ = json.NewDecoder(r.Body).Decode(&spec)
			key := parts[2] + "/" + parts[3]
			v := worker.Version{Model: parts[3], Version: itoa(len(f.versions[key]) + 1), Task: "classification",
				Dataset: spec.Dataset, Target: spec.Target, Features: []string{"age", "plan"},
				FeatureTypes: map[string]string{"age": "number", "plan": "string"},
				Metrics:      map[string]float64{"score": f.score, "accuracy": f.score}}
			f.versions[key] = append([]worker.Version{v}, f.versions[key]...)
			_ = json.NewEncoder(w).Encode(v)
		case r.Method == http.MethodGet && len(parts) == 5 && parts[4] == "versions":
			vs, ok := f.versions[parts[2]+"/"+parts[3]]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				_, _ = io.WriteString(w, `{"error":"no such model"}`)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"versions": vs})
		case r.Method == http.MethodDelete && len(parts) == 4 && parts[1] == "models":
			f.deleted = append(f.deleted, parts[2]+"/"+parts[3])
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && strings.HasSuffix(path, "/infer"):
			var req struct {
				Inputs []struct {
					Name string `json:"name"`
					Data []any  `json:"data"`
				} `json:"inputs"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			n := len(req.Inputs[0].Data)
			labels := make([]any, n)
			for i := range labels {
				labels[i] = "yes"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"outputs": []map[string]any{
				{"name": "label", "shape": []int{n}, "datatype": "BYTES", "data": labels},
			}})
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":"no route"}`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func workerService(t *testing.T, store *memStore, score float64) (*Service, *fakeWorker) {
	t.Helper()
	f := &fakeWorker{score: score, versions: map[string][]worker.Version{}}
	srv := f.server(t)
	svc := NewService(func() any { return store }, nil, nil).WithWorker(worker.New(srv.URL, "worker-token", nil))
	return svc, f
}

func TestTrainingANewModelRegistersItAndPutsItLive(t *testing.T) {
	store := newMemStore()
	svc, f := workerService(t, store, 0.91)

	res, err := svc.Train(t.Context(), "tenant-a", "churn",
		worker.TrainSpec{Dataset: "customers", Target: "churned"}, GoLive{Mode: GoLiveAlways}, "ada")
	if err != nil {
		t.Fatalf("Train: %v", err)
	}
	if !res.Live || res.Version.Version != "1" {
		t.Errorf("result = %+v, want version 1 live", res)
	}
	m, err := svc.Model(t.Context(), "tenant-a", "churn")
	if err != nil {
		t.Fatalf("the trained model was not registered: %v", err)
	}
	if m.Backend != storage.MLBackendWorker || m.RemoteVersion != "1" || len(m.Features) != 2 || m.UpdatedBy != "ada" {
		t.Errorf("registered model = %+v", m)
	}
	if m.FeatureTypes["age"] != "number" || m.FeatureTypes["plan"] != "string" {
		t.Errorf("the live version's feature types were not kept: %v", m.FeatureTypes)
	}
	if f.auth != "Bearer worker-token" {
		t.Errorf("the worker was called with %q", f.auth)
	}
}

func TestAVersionBelowTheBarIsKeptButNotPutLive(t *testing.T) {
	store := newMemStore()
	svc, f := workerService(t, store, 0.95)
	floor := 0.9
	rule := GoLive{Mode: GoLiveIf, Metric: "score", Min: &floor}

	if _, err := svc.Train(t.Context(), "v", "churn", worker.TrainSpec{Dataset: "d", Target: "y"}, rule, "ada"); err != nil {
		t.Fatal(err)
	}
	f.score = 0.7
	res, err := svc.Train(t.Context(), "v", "churn", worker.TrainSpec{Dataset: "d", Target: "y"}, rule, "ada")
	if err != nil {
		t.Fatal(err)
	}
	if res.Live || res.Version.Version != "2" || !strings.Contains(res.Reason, "0.7") {
		t.Errorf("result = %+v, want version 2 kept off with the score in the reason", res)
	}
	m, _ := svc.Model(t.Context(), "v", "churn")
	if m.RemoteVersion != "1" {
		t.Errorf("live version = %q, want version 1 to stay live", m.RemoteVersion)
	}
}

func TestGoLiveRules(t *testing.T) {
	lo, hi := 0.8, 10.0
	v := worker.Version{Metrics: map[string]float64{"score": 0.85, "rmse": 12}}
	for _, tc := range []struct {
		rule GoLive
		live bool
	}{
		{GoLive{Mode: GoLiveAlways}, true},
		{GoLive{Mode: GoLiveNever}, false},
		{GoLive{}, false},
		{GoLive{Mode: GoLiveIf, Min: &lo}, true},
		{GoLive{Mode: GoLiveIf, Metric: "rmse", Max: &hi}, false},
		{GoLive{Mode: GoLiveIf, Metric: "f1", Min: &lo}, false},
	} {
		live, reason := tc.rule.decide(v)
		if live != tc.live || reason == "" {
			t.Errorf("%+v: live = %v (%q), want %v and a reason", tc.rule, live, reason, tc.live)
		}
	}
	if err := (GoLive{Mode: GoLiveIf}).Validate(); err == nil {
		t.Error("an 'if' rule with no bound was accepted")
	}
	if err := (GoLive{Mode: "sometimes"}).Validate(); err == nil {
		t.Error("an unknown rule was accepted")
	}
}

func TestTrainingRefusesANameAnExternalModelHolds(t *testing.T) {
	store := newMemStore(storage.MLModel{VHost: "v", Name: "fraud", Backend: inference.BackendOIP, URL: "http://kserve", RemoteModel: "fraud"})
	svc, f := workerService(t, store, 0.9)
	_, err := svc.Train(t.Context(), "v", "fraud", worker.TrainSpec{Dataset: "d", Target: "y"}, GoLive{Mode: GoLiveAlways}, "ada")
	if err == nil || !strings.Contains(err.Error(), "already") {
		t.Errorf("err = %v, want a refusal naming the existing model", err)
	}
	if len(f.calls) != 0 {
		t.Errorf("the worker was called: %v", f.calls)
	}
}

func TestPredictCallsTheLiveVersionOnTheWorker(t *testing.T) {
	store := newMemStore(storage.MLModel{VHost: "tenant-a", Name: "churn", Backend: storage.MLBackendWorker, RemoteVersion: "2", Features: []string{"age"}})
	svc, f := workerService(t, store, 0)

	out, err := svc.Predict(t.Context(), "tenant-a", "churn", []inference.Row{{"age": 40.0}})
	if err != nil {
		t.Fatalf("Predict: %v", err)
	}
	if len(out) != 1 || out[0]["label"] != "yes" {
		t.Errorf("predictions = %v", out)
	}
	if got := f.calls[len(f.calls)-1]; got != "POST /vhosts/tenant-a/v2/models/churn/versions/2/infer" {
		t.Errorf("called %s", got)
	}
	if f.auth != "Bearer worker-token" {
		t.Errorf("Authorization = %q, want the worker's token", f.auth)
	}
}

func TestPredictRefusesAWorkerModelWithNothingLiveOrNoWorker(t *testing.T) {
	store := newMemStore(storage.MLModel{VHost: "v", Name: "churn", Backend: storage.MLBackendWorker})
	svc, _ := workerService(t, store, 0)
	if _, err := svc.Predict(t.Context(), "v", "churn", []inference.Row{{"a": 1.0}}); !errors.Is(err, ErrNoLiveVersion) {
		t.Errorf("err = %v, want ErrNoLiveVersion", err)
	}

	store = newMemStore(storage.MLModel{VHost: "v", Name: "churn", Backend: storage.MLBackendWorker, RemoteVersion: "1"})
	noWorker := NewService(func() any { return store }, nil, nil).WithWorker(nil)
	if _, err := noWorker.Predict(t.Context(), "v", "churn", []inference.Row{{"a": 1.0}}); !errors.Is(err, ErrNoWorker) {
		t.Errorf("err = %v, want ErrNoWorker", err)
	}
	if _, err := noWorker.Train(t.Context(), "v", "x", worker.TrainSpec{Dataset: "d", Target: "y"}, GoLive{}, "ada"); !errors.Is(err, ErrNoWorker) {
		t.Errorf("Train without a worker: err = %v, want ErrNoWorker", err)
	}
}

func TestPromotePutsAnExistingVersionLive(t *testing.T) {
	store := newMemStore()
	svc, _ := workerService(t, store, 0.9)
	for range 2 {
		if _, err := svc.Train(t.Context(), "v", "churn", worker.TrainSpec{Dataset: "d", Target: "y"}, GoLive{Mode: GoLiveAlways}, "ada"); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.Promote(t.Context(), "v", "churn", "1", "bob"); err != nil {
		t.Fatalf("rolling back to version 1: %v", err)
	}
	if m, _ := svc.Model(t.Context(), "v", "churn"); m.RemoteVersion != "1" || m.UpdatedBy != "bob" {
		t.Errorf("model = %+v, want version 1 live, put there by bob", m)
	}
	if err := svc.Promote(t.Context(), "v", "churn", "9", "bob"); !errors.Is(err, ErrVersionNotFound) {
		t.Errorf("promoting a version that does not exist: err = %v", err)
	}
}

func TestDeletingAWorkerModelDeletesItsVersionsToo(t *testing.T) {
	store := newMemStore(
		storage.MLModel{VHost: "v", Name: "churn", Backend: storage.MLBackendWorker, RemoteVersion: "1"},
		storage.MLModel{VHost: "v", Name: "fraud", Backend: inference.BackendOIP, URL: "http://kserve", RemoteModel: "fraud"},
	)
	svc, f := workerService(t, store, 0)
	if err := svc.DeleteModel(t.Context(), "v", "churn"); err != nil {
		t.Fatalf("DeleteModel: %v", err)
	}
	if err := svc.DeleteModel(t.Context(), "v", "fraud"); err != nil {
		t.Fatalf("DeleteModel: %v", err)
	}
	if len(f.deleted) != 1 || f.deleted[0] != "v/churn" {
		t.Errorf("worker deletes = %v, want only v/churn", f.deleted)
	}
	if _, err := svc.Model(t.Context(), "v", "churn"); !errors.Is(err, ErrModelNotFound) {
		t.Errorf("churn is still registered: %v", err)
	}
	if err := svc.DeleteModel(t.Context(), "v", "churn"); !errors.Is(err, ErrModelNotFound) {
		t.Errorf("deleting twice: err = %v", err)
	}
}
