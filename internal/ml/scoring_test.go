package ml

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/inference"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

func (s *memStore) SetMLModelScoring(_ context.Context, vhost, name, scoring string) error {
	if err := storage.ValidateMLScoring(scoring); err != nil {
		return err
	}
	return s.edit(vhost, name, func(m *storage.MLModel) { m.Scoring = scoring })
}

// resetScorers empties the scorer cache, so each test loads its own.
func resetScorers() {
	scorers.mu.Lock()
	defer scorers.mu.Unlock()
	scorers.entries = map[string]*scorerEntry{}
}

// fixtures is where pkg/ml/onnxscore keeps the models the worker trained for
// its parity tests, with what the worker answered for them.
const fixtures = "../../pkg/ml/onnxscore/testdata"

// scoringWorker is a hermod-ml worker holding version 1 of model "churn" in
// vhost "v": the files of one parity fixture. It counts what it is asked.
type scoringWorker struct {
	meta     json.RawMessage
	onnx     []byte
	files    atomic.Int32
	versions atomic.Int32
	infers   atomic.Int32
}

func newScoringWorker(t *testing.T, fixture string) (*scoringWorker, *worker.Client) {
	t.Helper()
	dir := filepath.Join(fixtures, fixture)
	w := &scoringWorker{}
	var err error
	if w.meta, err = os.ReadFile(filepath.Join(dir, "meta.json")); err != nil {
		t.Fatal(err)
	}
	// The fixture's meta says version 1, as the worker's store numbers it.
	if w.onnx, err = os.ReadFile(filepath.Join(dir, "model.onnx")); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models/v/churn/versions":
			w.versions.Add(1)
			_, _ = rw.Write([]byte(`{"versions":[` + string(w.meta) + `]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models/v/churn/versions/1/model.onnx":
			w.files.Add(1)
			_, _ = rw.Write(w.onnx)
		case r.Method == http.MethodPost && r.URL.Path == "/vhosts/v/v2/models/churn/versions/1/infer":
			w.infers.Add(1)
			var req struct {
				Inputs []struct {
					Shape []int `json:"shape"`
				} `json:"inputs"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			n := req.Inputs[0].Shape[0]
			data := make([]any, n)
			for i := range data {
				data[i] = "from-worker"
			}
			_ = json.NewEncoder(rw).Encode(map[string]any{"outputs": []map[string]any{
				{"name": "label", "datatype": "BYTES", "shape": []int{n}, "data": data},
			}})
		default:
			http.NotFound(rw, r)
		}
	}))
	t.Cleanup(srv.Close)
	return w, worker.New(srv.URL, "", nil)
}

func trainedChurn(scoring string) storage.MLModel {
	return storage.MLModel{
		VHost: "v", Name: "churn", Backend: storage.MLBackendWorker, RemoteVersion: "1",
		Features: []string{"x1", "x2", "city", "flag"}, Scoring: scoring,
	}
}

// workerCases reads the rows of a fixture and what the worker answered.
func workerCases(t *testing.T, fixture string) ([]inference.Row, []inference.Row) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(fixtures, fixture, "cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Rows        []inference.Row `json:"rows"`
		Predictions []inference.Row `json:"predictions"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	return cases[0].Rows, cases[0].Predictions
}

// TestInProcessScoringIsReachedFromTheStoredSetting starts where an operator
// does — a stored model whose scoring is in_process — and goes through
// Predict: the predictions are the worker's own (to tolerance), the worker
// is never asked to infer, and the graph is fetched once, not per call.
func TestInProcessScoringIsReachedFromTheStoredSetting(t *testing.T) {
	resetScorers()
	w, client := newScoringWorker(t, "trained/random_forest_binary")
	st := newMemStore(trainedChurn(storage.MLScoringInProcess))
	svc := NewService(func() any { return st }, nil, nil).WithWorker(client)
	rows, want := workerCases(t, "trained/random_forest_binary")

	for range 3 {
		got, err := svc.Predict(t.Context(), "v", "churn", rows)
		if err != nil {
			t.Fatalf("Predict: %v", err)
		}
		for i := range want {
			if got[i]["label"] != want[i]["label"] {
				t.Fatalf("row %d: %v, the worker said %v", i, got[i], want[i])
			}
			if d := got[i]["probability"].(float64) - want[i]["probability"].(float64); d > 1e-5 || d < -1e-5 {
				t.Fatalf("row %d: %v, the worker said %v", i, got[i], want[i])
			}
		}
	}
	if n := w.infers.Load(); n != 0 {
		t.Errorf("the worker was asked to infer %d times", n)
	}
	if n := w.files.Load(); n != 1 {
		t.Errorf("the model file was fetched %d times, want once", n)
	}

	// Breaking the wiring — the stored setting back to the worker — sends
	// every call to the worker again.
	if err := svc.SetScoring(t.Context(), "v", "churn", storage.MLScoringWorker); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Predict(t.Context(), "v", "churn", rows[:1])
	if err != nil || got[0]["label"] != "from-worker" || w.infers.Load() != 1 {
		t.Errorf("with scoring on the worker: %v, %v (%d infers)", got, err, w.infers.Load())
	}
}

func TestTheWorkerScoresByDefault(t *testing.T) {
	resetScorers()
	w, client := newScoringWorker(t, "trained/linear_binary")
	svc := NewService(func() any { return newMemStore(trainedChurn("")) }, nil, nil).WithWorker(client)
	rows, _ := workerCases(t, "trained/linear_binary")
	if _, err := svc.Predict(t.Context(), "v", "churn", rows); err != nil {
		t.Fatal(err)
	}
	if w.infers.Load() != 1 || w.files.Load() != 0 {
		t.Errorf("infers %d, files %d: the default must not touch the model file", w.infers.Load(), w.files.Load())
	}
}

// TestAnUnsupportedGraphFallsBackToTheWorker: a graph the scorer refuses is
// scored by the worker, and the status says why; the refusal is remembered,
// so the file is not fetched again on every call.
func TestAnUnsupportedGraphFallsBackToTheWorker(t *testing.T) {
	resetScorers()
	w, client := newScoringWorker(t, "trained/linear_binary")
	if raw, err := os.ReadFile(filepath.Join(fixtures, "graphs", "unsupported_op.onnx")); err == nil {
		w.onnx = raw
	} else {
		t.Fatal(err)
	}
	svc := NewService(func() any { return newMemStore(trainedChurn(storage.MLScoringInProcess)) }, nil, nil).WithWorker(client)
	rows, _ := workerCases(t, "trained/linear_binary")
	for range 2 {
		got, err := svc.Predict(t.Context(), "v", "churn", rows)
		if err != nil || got[0]["label"] != "from-worker" {
			t.Fatalf("Predict = %v, %v; want the worker's answer", got, err)
		}
	}
	if w.files.Load() != 1 {
		t.Errorf("the model file was fetched %d times, want once", w.files.Load())
	}
	status, err := svc.Scoring(t.Context(), "v", "churn")
	if err != nil {
		t.Fatal(err)
	}
	if status.Scoring != storage.MLScoringInProcess || status.InProcess || !strings.Contains(status.Reason, "Abs") {
		t.Errorf("status = %+v, want in_process configured, not active, naming Abs", status)
	}
}

// TestRowsTheScorerCannotReadGoToTheWorker: one value the scorer would have
// to guess at sends the whole call to the worker.
func TestRowsTheScorerCannotReadGoToTheWorker(t *testing.T) {
	resetScorers()
	w, client := newScoringWorker(t, "trained/linear_binary")
	svc := NewService(func() any { return newMemStore(trainedChurn(storage.MLScoringInProcess)) }, nil, nil).WithWorker(client)
	rows := []inference.Row{{"x1": 1.0, "x2": 2.0, "city": map[string]any{"nested": true}, "flag": true}}
	got, err := svc.Predict(t.Context(), "v", "churn", rows)
	if err != nil || got[0]["label"] != "from-worker" || w.infers.Load() != 1 {
		t.Errorf("Predict = %v, %v (%d infers); want the worker's answer", got, err, w.infers.Load())
	}
}

func TestScoringStatusOfASupportedModel(t *testing.T) {
	resetScorers()
	_, client := newScoringWorker(t, "trained/xgboost_regression")
	svc := NewService(func() any { return newMemStore(trainedChurn(storage.MLScoringInProcess)) }, nil, nil).WithWorker(client)
	status, err := svc.Scoring(t.Context(), "v", "churn")
	if err != nil {
		t.Fatal(err)
	}
	if !status.InProcess || status.Version != "1" || status.Reason != "" ||
		!strings.Contains(strings.Join(status.Ops, ","), "ai.onnx.ml.TreeEnsembleRegressor") {
		t.Errorf("status = %+v", status)
	}
}

func TestSetScoringOnlyForATrainedModel(t *testing.T) {
	external := storage.MLModel{VHost: "v", Name: "fraud", Backend: inference.BackendOIP, URL: "http://ml:8080", RemoteModel: "fraud"}
	st := newMemStore(trainedChurn(""), external)
	svc := NewService(func() any { return st }, nil, nil).WithWorker(nil)
	ctx := t.Context()

	if err := svc.SetScoring(ctx, "v", "fraud", storage.MLScoringInProcess); !errors.Is(err, ErrScoringNotTrained) {
		t.Errorf("an external model: %v, want ErrScoringNotTrained", err)
	}
	if err := svc.SetScoring(ctx, "v", "nope", storage.MLScoringInProcess); !errors.Is(err, ErrModelNotFound) {
		t.Errorf("a missing model: %v, want ErrModelNotFound", err)
	}
	if err := svc.SetScoring(ctx, "v", "churn", "gpu"); err == nil {
		t.Error("an unknown mode was accepted")
	}
	if err := svc.SetScoring(ctx, "v", "churn", storage.MLScoringInProcess); err != nil {
		t.Fatal(err)
	}
	if m, _ := svc.Model(ctx, "v", "churn"); m.Scoring != storage.MLScoringInProcess {
		t.Errorf("stored scoring = %q", m.Scoring)
	}
}

// An in-process prediction is a prediction like any other: it is logged with
// its caller and version, and counted for drift against the version's
// training stats, exactly as one the worker scored would be.
func TestAnInProcessPredictionIsLoggedAndCountedForDrift(t *testing.T) {
	resetScorers()
	w, client := newScoringWorker(t, "trained/random_forest_binary")
	var meta map[string]any
	if err := json.Unmarshal(w.meta, &meta); err != nil {
		t.Fatal(err)
	}
	meta["feature_stats"] = map[string]worker.FeatureStats{"x1": {Kind: worker.StatsNumeric, Count: 100,
		Edges: []float64{0}, Fractions: []float64{0.5, 0.5}}}
	w.meta, _ = json.Marshal(meta)
	m := trainedChurn(storage.MLScoringInProcess)
	m.Monitoring = storage.MLMonitoring{LogSampleRate: 1}
	st := newMemStore(m)
	logs := &predLogs{}
	svc := NewService(func() any { return st }, nil, nil).WithWorker(client).WithLogStore(func() any { return logs })
	startMonitor(t, svc, logs)
	rows, _ := workerCases(t, "trained/random_forest_binary")

	if _, err := svc.Predict(WithCaller(t.Context(), storage.MLCallerREST, ""), "v", "churn", rows); err != nil {
		t.Fatalf("Predict: %v", err)
	}
	if n := w.infers.Load(); n != 0 {
		t.Fatalf("the worker was asked to infer %d times; the test needs an in-process prediction", n)
	}
	waitFor(t, "the predictions to be logged", func() bool { return logs.count() == len(rows) })
	logged, err := svc.PredictionLogs(t.Context(), "v", "churn", 1)
	if err != nil || len(logged) != 1 {
		t.Fatalf("PredictionLogs = %+v, %v", logged, err)
	}
	if l := logged[0]; l.CallerKind != storage.MLCallerREST || l.Version != "1" || l.Outputs["label"] == nil {
		t.Errorf("logged %+v", l)
	}

	var drift DriftStatus
	waitFor(t, "a drift report", func() bool {
		drift, _ = svc.Drift(t.Context(), "v", "churn")
		return drift.Report != nil
	})
	if r := drift.Report; r.Version != "1" || r.Rows != int64(len(rows)) {
		t.Errorf("drift report = %+v, want version 1 over %d rows", r, len(rows))
	}
}
