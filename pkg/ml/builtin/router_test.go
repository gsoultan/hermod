package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// fakeWorker is a hermod-ml worker over HTTP: it keeps datasets and numbers
// its versions 1, 2, …, and logs what it was asked.
type fakeWorker struct {
	mu       sync.Mutex
	calls    []string
	datasets map[string][]map[string]any
	versions map[string][]worker.Version
}

func newFakeWorker(t *testing.T) (*fakeWorker, *worker.Client) {
	t.Helper()
	f := &fakeWorker{datasets: map[string][]map[string]any{}, versions: map[string][]worker.Version{}}
	m := http.NewServeMux()
	m.HandleFunc("GET /v2/health/ready", func(w http.ResponseWriter, _ *http.Request) { reply(w, map[string]bool{"ready": true}) })
	m.HandleFunc("GET /v1/capabilities", func(w http.ResponseWriter, _ *http.Request) {
		reply(w, worker.Capabilities{Tasks: []string{"auto", "classification", "regression", "recommendation"},
			Algorithms:  []string{"auto", "random_forest", "linear", "xgboost", "extra_trees", "als", "item_similarity", "pytorch_mlp"},
			Unavailable: map[string]string{"keras_mlp": "TensorFlow is not installed"}})
	})
	m.HandleFunc("POST /v1/datasets/{vhost}/{name}/rows", f.appendRows)
	m.HandleFunc("GET /v1/datasets/{vhost}", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		list := []worker.DatasetInfo{}
		for name, rows := range f.datasets {
			list = append(list, worker.DatasetInfo{Name: name, Rows: len(rows)})
		}
		reply(w, map[string]any{"datasets": list})
	})
	m.HandleFunc("GET /v1/datasets/{vhost}/{name}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		rows, ok := f.datasets[r.PathValue("name")]
		if !ok {
			fail(w, notFound("Dataset %q does not exist.", r.PathValue("name")))
			return
		}
		reply(w, worker.DatasetInfo{Name: r.PathValue("name"), Rows: len(rows)})
	})
	m.HandleFunc("DELETE /v1/datasets/{vhost}/{name}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if _, ok := f.datasets[r.PathValue("name")]; !ok {
			fail(w, notFound("Dataset %q does not exist.", r.PathValue("name")))
			return
		}
		delete(f.datasets, r.PathValue("name"))
		w.WriteHeader(http.StatusNoContent)
	})
	m.HandleFunc("POST /v1/models/{vhost}/{name}/train", f.train)
	m.HandleFunc("GET /v1/models/{vhost}/{name}/versions", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		vs, ok := f.versions[r.PathValue("name")]
		if !ok {
			fail(w, notFound("Model %q has no versions.", r.PathValue("name")))
			return
		}
		reply(w, map[string]any{"versions": vs})
	})
	m.HandleFunc("GET /v1/models/{vhost}/{name}/versions/{version}/model.rec", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("worker-rec-" + r.PathValue("version")))
	})
	m.HandleFunc("DELETE /v1/models/{vhost}/{name}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if _, ok := f.versions[r.PathValue("name")]; !ok {
			fail(w, notFound("Model %q has no versions.", r.PathValue("name")))
			return
		}
		delete(f.versions, r.PathValue("name"))
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.calls = append(f.calls, r.Method+" "+r.URL.Path)
		f.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer tok" {
			fail(w, &apiError{status: http.StatusUnauthorized, msg: "bad token"})
			return
		}
		m.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return f, worker.New(srv.URL, "tok", srv.Client())
}

func (f *fakeWorker) appendRows(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Rows    []map[string]any `json:"rows"`
		Replace bool             `json:"replace"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		fail(w, badRequest("bad body"))
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	name := r.PathValue("name")
	if body.Replace {
		f.datasets[name] = nil
	}
	f.datasets[name] = append(f.datasets[name], body.Rows...)
	reply(w, map[string]any{"name": name, "rows": len(f.datasets[name])})
}

func (f *fakeWorker) train(w http.ResponseWriter, r *http.Request) {
	var spec worker.TrainSpec
	_ = json.NewDecoder(r.Body).Decode(&spec)
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.datasets[spec.Dataset]; !ok {
		fail(w, notFound("Dataset %q does not exist.", spec.Dataset))
		return
	}
	name := r.PathValue("name")
	v := worker.Version{Model: name, Version: strconv.Itoa(len(f.versions[name]) + 1), Task: "regression", Algorithm: spec.Algorithm}
	f.versions[name] = append([]worker.Version{v}, f.versions[name]...)
	reply(w, v)
}

// called counts the calls whose method and path start with prefix.
func (f *fakeWorker) called(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

// routed is the built-in engine routing to a fake worker.
func routed(t *testing.T) (*Engine, *fakeWorker, *worker.Client) {
	t.Helper()
	e, _ := newEngine(t)
	f, remote := newFakeWorker(t)
	return e, f, e.Route(remote)
}

func TestABuiltinAlgorithmTrainsInProcessWithAWorkerConnected(t *testing.T) {
	_, f, c := routed(t)
	appendRows(t, c, "lin", linearRows(100))
	v, err := c.Train(context.Background(), "v", "m", worker.TrainSpec{Dataset: "lin", Target: "y", Algorithm: "linear"})
	if err != nil {
		t.Fatal(err)
	}
	if v.Version != "b1" || v.Engine != worker.EngineBuiltin {
		t.Errorf("version = %s by %q, want b1 by the built-in engine", v.Version, v.Engine)
	}
	if n := f.called("POST /v1/models/"); n != 0 {
		t.Errorf("the worker was asked to train %d times", n)
	}
	if n := f.called("POST /v1/datasets/"); n != 0 {
		t.Errorf("the dataset went to the worker (%d calls); it belongs in Hermod's store", n)
	}
}

func TestWorkerOnlyAlgorithmsAndAutoTrainOnTheWorker(t *testing.T) {
	_, f, c := routed(t)
	ctx := context.Background()
	appendRows(t, c, "lin", linearRows(5000))
	for i, algorithm := range []string{"xgboost", "auto", ""} {
		v, err := c.Train(ctx, "v", "m", worker.TrainSpec{Dataset: "lin", Target: "y", Algorithm: algorithm})
		if err != nil {
			t.Fatalf("Train %q: %v", algorithm, err)
		}
		if want := strconv.Itoa(i + 1); v.Version != want || v.Engine != worker.EngineWorker {
			t.Errorf("Train %q = version %s by %q, want %s by the worker", algorithm, v.Version, v.Engine, want)
		}
	}
	f.mu.Lock()
	rows := len(f.datasets["lin"])
	f.mu.Unlock()
	if rows != 5000 {
		t.Errorf("the worker trained on %d rows, want the dataset's 5000", rows)
	}
	syncs := f.called("POST /v1/datasets/v/lin/rows")
	if syncs == 0 || syncs > 5000/syncChunkRows+1 {
		t.Errorf("the dataset went over in %d calls", syncs)
	}
	appendRows(t, c, "lin", linearRows(10)) // replaces it
	if _, err := c.Train(ctx, "v", "m", worker.TrainSpec{Dataset: "lin", Target: "y", Algorithm: "xgboost"}); err != nil {
		t.Fatal(err)
	}
	if n := f.called("POST /v1/datasets/v/lin/rows"); n != syncs+1 {
		t.Errorf("a changed dataset went over in %d more calls, want 1 (an unchanged one in none)", n-syncs)
	}
	f.mu.Lock()
	rows = len(f.datasets["lin"])
	f.mu.Unlock()
	if rows != 10 {
		t.Errorf("after the dataset changed, the worker trained on %d rows, want 10", rows)
	}
}

func TestAutoTrainsInProcessWithoutAWorker(t *testing.T) {
	e, _ := newEngine(t)
	c := e.Route(nil)
	appendRows(t, c, "lin", linearRows(100))
	v, err := c.Train(context.Background(), "v", "m", worker.TrainSpec{Dataset: "lin", Target: "y", Algorithm: "auto", Preset: "quick"})
	if err != nil || v.Version != "b1" {
		t.Errorf("auto with no worker = %s, %v; want b1", v.Version, err)
	}
	if c.Remote() != nil {
		t.Error("the engine alone reports a remote worker")
	}
	if _, err := c.Train(context.Background(), "v", "m", worker.TrainSpec{Dataset: "lin", Target: "y", Algorithm: "xgboost"}); err == nil ||
		!strings.Contains(err.Error(), NeedsWorker) {
		t.Errorf("xgboost with no worker: %v, want %q", err, NeedsWorker)
	}
}

func TestEachVersionIsServedByTheEngineThatTrainedIt(t *testing.T) {
	_, f, c := routed(t)
	ctx := context.Background()
	appendRows(t, c, "baskets", basketRows(60, 8))
	if _, err := c.Train(ctx, "v", "rec", recommendSpec("item_similarity")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Train(ctx, "v", "rec", recommendSpec("auto")); err != nil {
		t.Fatal(err)
	}
	vs, err := c.Versions(ctx, "v", "rec")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, v := range vs {
		got = append(got, v.Version+"/"+v.Engine)
	}
	slices.Sort(got)
	if strings.Join(got, " ") != "1/worker b1/builtin" {
		t.Errorf("versions = %v, want both engines' versions, each with its engine", got)
	}
	raw, err := c.RecommenderFile(ctx, "v", "rec", "1")
	if err != nil || string(raw) != "worker-rec-1" {
		t.Errorf("version 1 = %q, %v; want the worker's artifact", raw, err)
	}
	before := f.called("GET")
	if _, err := c.RecommenderFile(ctx, "v", "rec", "b1"); err != nil {
		t.Errorf("version b1: %v", err)
	}
	if f.called("GET") != before {
		t.Error("the worker was asked for the built-in engine's version")
	}
	if err := c.DeleteModel(ctx, "v", "rec"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Versions(ctx, "v", "rec"); !errors.Is(err, worker.ErrNotFound) {
		t.Errorf("after deleting, versions: %v; want both engines' gone", err)
	}
}

func TestAWorkerVersionWithoutTheWorkerSaysItNeedsIt(t *testing.T) {
	e, c := newEngine(t)
	ctx := context.Background()
	_, err := c.RecommenderFile(ctx, "v", "rec", "3")
	if err == nil || !strings.Contains(err.Error(), NeedsWorker) || errors.Is(err, worker.ErrNotFound) {
		t.Errorf("a worker's version with no worker: %v; want %q, not not-found", err, NeedsWorker)
	}
	if _, err := e.Predict(ctx, "v", "m", "3", []map[string]any{{"a": 1.0}}); err == nil || !strings.Contains(err.Error(), NeedsWorker) {
		t.Errorf("predicting with a worker's version with no worker: %v; want %q", err, NeedsWorker)
	}
}

func TestDatasetsAreTheUnionWithTheBuiltinCopyWinning(t *testing.T) {
	e, f, c := routed(t)
	ctx := context.Background()
	f.datasets["legacy"] = linearRows(7)
	f.datasets["lin"] = linearRows(3) // a copy the worker was sent
	appendRows(t, e.Client(), "lin", linearRows(20))

	list, err := c.Datasets(ctx, "v")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range list {
		got = append(got, d.Name+"/"+d.Engine+"/"+strconv.Itoa(d.Rows))
	}
	slices.Sort(got)
	if strings.Join(got, " ") != "legacy/worker/7 lin/builtin/20" {
		t.Errorf("datasets = %v", got)
	}
	if d, err := c.Dataset(ctx, "v", "legacy"); err != nil || d.Engine != worker.EngineWorker || d.Rows != 7 {
		t.Errorf("legacy = %+v, %v", d, err)
	}
	// Rows for a dataset only the worker has go on to it.
	if n, err := c.AppendRows(ctx, "v", "legacy", linearRows(2), false); err != nil || n != 9 {
		t.Errorf("appending to legacy = %d, %v; want 9 on the worker", n, err)
	}
	// A dataset only the worker has trains there, whatever the algorithm.
	if v, err := c.Train(ctx, "v", "m", worker.TrainSpec{Dataset: "legacy", Target: "y", Algorithm: "linear"}); err != nil || v.Engine != worker.EngineWorker {
		t.Errorf("training on legacy = %+v, %v; want the worker's version", v, err)
	}
	if err := c.DeleteDataset(ctx, "v", "lin"); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.datasets["lin"]; ok {
		t.Error("deleting lin left the worker's copy")
	}
	if _, err := c.Dataset(ctx, "v", "lin"); !errors.Is(err, worker.ErrNotFound) {
		t.Errorf("lin after deleting: %v", err)
	}
}

func TestCapabilitiesAreBothEngines(t *testing.T) {
	_, _, c := routed(t)
	caps, err := c.Capabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range []string{"linear", "mlp", "gradient_boosting", "xgboost", "extra_trees", "pytorch_mlp"} {
		if !slices.Contains(caps.Algorithms, a) {
			t.Errorf("algorithms %v lack %s", caps.Algorithms, a)
		}
	}
	if _, ok := caps.Unavailable["xgboost"]; ok {
		t.Error("xgboost is unavailable although the worker trains it")
	}
	if caps.Unavailable["keras_mlp"] != "TensorFlow is not installed" {
		t.Errorf("keras_mlp unavailable = %q, want the worker's reason", caps.Unavailable["keras_mlp"])
	}
	if caps.Unavailable["hist_gradient_boosting"] == "" {
		t.Error("an algorithm neither engine lists is not unavailable")
	}
}

func TestAWorkerThatIsDownLeavesTheBuiltinEngineWorking(t *testing.T) {
	e, _ := newEngine(t)
	srv := httptest.NewServer(http.NotFoundHandler())
	down := worker.New(srv.URL, "tok", srv.Client())
	srv.Close()
	c := e.Route(down)
	ctx := context.Background()
	appendRows(t, c, "lin", linearRows(50))
	if list, err := c.Datasets(ctx, "v"); err != nil || len(list) != 1 {
		t.Errorf("datasets = %v, %v; want the built-in one", list, err)
	}
	if _, err := c.Train(ctx, "v", "m", worker.TrainSpec{Dataset: "lin", Target: "y", Algorithm: "linear"}); err != nil {
		t.Errorf("training in-process: %v", err)
	}
	if vs, err := c.Versions(ctx, "v", "m"); err != nil || len(vs) != 1 {
		t.Errorf("versions = %v, %v", vs, err)
	}
	if caps, err := c.Capabilities(ctx); err != nil || !slices.Contains(caps.Algorithms, "linear") {
		t.Errorf("capabilities = %+v, %v; want the built-in engine's", caps, err)
	}
	if err := c.Ready(ctx); err != nil {
		t.Errorf("Ready: %v; the built-in engine is", err)
	}
}
