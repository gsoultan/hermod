package ml

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// poolWorker is a hermod-ml worker that answers the transfer and training
// routes a pool training uses, and records what it was sent.
type poolWorker struct {
	mu        sync.Mutex
	calls     []string
	bodies    map[string]string // "METHOD path" -> body
	failTrain bool
}

func (p *poolWorker) start(t *testing.T, token string) *worker.Client {
	t.Helper()
	p.bodies = map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		defer p.mu.Unlock()
		path := r.URL.EscapedPath()
		call := r.Method + " " + path
		p.calls = append(p.calls, call)
		body, _ := io.ReadAll(r.Body)
		p.bodies[call] = string(body)
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		parts := strings.Split(strings.Trim(path, "/"), "/")
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(path, "/export") && parts[1] == "datasets":
			_, _ = io.WriteString(w, "PARQUET:"+parts[3])
		case r.Method == http.MethodPut && strings.HasSuffix(path, "/import"):
			_, _ = io.WriteString(w, `{"name":"`+parts[3]+`","rows":3,"columns":[],"updated_at":"2026-10-10T00:00:00Z"}`)
		case r.Method == http.MethodPost && (strings.HasSuffix(path, "/train") || strings.HasSuffix(path, "/train-custom")):
			if p.failTrain {
				w.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = io.WriteString(w, `{"error":"The training script failed: ValueError: boom"}`)
				return
			}
			_, _ = io.WriteString(w, `{"model":"`+parts[3]+`","version":"1","log":"trained on the pool"}`)
		case r.Method == http.MethodGet && strings.HasSuffix(path, "/export"):
			_, _ = io.WriteString(w, `{"meta":{"from":"pool"},"onnx":"AAAA"}`)
		case r.Method == http.MethodPost && strings.HasSuffix(path, "/import"):
			p.bodies[call+"?"+r.URL.RawQuery] = string(body)
			_ = json.NewEncoder(w).Encode(worker.Version{Model: parts[3], Version: "7", Dataset: r.URL.Query().Get("dataset"),
				Target: "churned", Features: []string{"age"}, Metrics: map[string]float64{"score": 0.9}, Log: "trained on the pool"})
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":"no route"}`)
		}
	}))
	t.Cleanup(srv.Close)
	return worker.New(srv.URL, token, nil)
}

func (p *poolWorker) called(prefix string) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	for _, c := range p.calls {
		if strings.HasPrefix(c, prefix) {
			out = append(out, c)
		}
	}
	return out
}

// scriptStore adds an in-memory MLScriptStore to memStore.
type scriptStore struct {
	*memStore
	scripts map[string]storage.MLScript
}

func newScriptStore(scripts ...storage.MLScript) *scriptStore {
	s := &scriptStore{memStore: newMemStore(), scripts: map[string]storage.MLScript{}}
	for _, sc := range scripts {
		sc.SHA256 = storage.MLScriptSHA256(sc.Source)
		s.scripts[sc.VHost+"/"+sc.Name] = sc
	}
	return s
}

func (s *scriptStore) ListMLScripts(_ context.Context, vhost string) ([]storage.MLScript, error) {
	var out []storage.MLScript
	for _, sc := range s.scripts {
		if sc.VHost == vhost {
			sc.Source = ""
			out = append(out, sc)
		}
	}
	return out, nil
}
func (s *scriptStore) ListMLScriptVersions(context.Context, string, string) ([]storage.MLScript, error) {
	return nil, nil
}
func (s *scriptStore) GetMLScript(_ context.Context, vhost, name string) (storage.MLScript, error) {
	sc, ok := s.scripts[vhost+"/"+name]
	if !ok {
		return storage.MLScript{}, storage.ErrNotFound
	}
	return sc, nil
}
func (s *scriptStore) PutMLScript(_ context.Context, sc storage.MLScript) (storage.MLScript, error) {
	sc.SHA256, sc.Version = storage.MLScriptSHA256(sc.Source), 1
	s.scripts[sc.VHost+"/"+sc.Name] = sc
	return sc, nil
}
func (s *scriptStore) DeleteMLScript(_ context.Context, vhost, name string) error {
	if _, ok := s.scripts[vhost+"/"+name]; !ok {
		return storage.ErrNotFound
	}
	delete(s.scripts, vhost+"/"+name)
	return nil
}
func (s *scriptStore) DeleteMLScripts(context.Context, string) error { return nil }

const treesScript = "def train(df, spec):\n    ...\n"

type poolSetup struct {
	svc               *Service
	main, custom, gpu *poolWorker
}

func newPoolSetup(t *testing.T, pools func(custom, gpu *worker.Client) Pools) poolSetup {
	t.Helper()
	ps := poolSetup{main: &poolWorker{}, custom: &poolWorker{}, gpu: &poolWorker{}}
	store := newScriptStore(storage.MLScript{VHost: "tenant-a", Name: "trees", Source: treesScript, Version: 3})
	ps.svc = NewService(func() any { return store }, nil, nil).
		WithWorker(ps.main.start(t, "main-token")).
		WithPools(pools(ps.custom.start(t, "custom-token"), ps.gpu.start(t, "gpu-token")))
	return ps
}

func allPools(custom, gpu *worker.Client) Pools {
	return Pools{Custom: custom, CustomScripts: true, GPU: gpu}
}

func TestAGPUTrainingRunsOnTheGPUPoolAndIsServedByTheMainWorker(t *testing.T) {
	ps := newPoolSetup(t, allPools)
	res, err := ps.svc.Train(t.Context(), "tenant-a", "churn",
		worker.TrainSpec{Dataset: "customers", Target: "churned", Device: DeviceGPU}, GoLive{Mode: GoLiveAlways}, "ada")
	if err != nil {
		t.Fatalf("Train: %v", err)
	}
	if res.Version.Version != "7" || res.Version.Dataset != "customers" || !res.Live {
		t.Errorf("result = %+v, want the main worker's version 7 of dataset customers, live", res)
	}

	// The dataset went main -> GPU pool, the version GPU pool -> main.
	if got := ps.main.called("GET /v1/datasets/tenant-a/customers/export"); len(got) != 1 {
		t.Errorf("main worker calls = %v", ps.main.calls)
	}
	imports := ps.gpu.called("PUT /v1/datasets/tenant-a/job-")
	if len(imports) != 1 || ps.gpu.bodies[imports[0]] != "PARQUET:customers" {
		t.Fatalf("GPU pool imports = %v (%v)", imports, ps.gpu.calls)
	}
	job := strings.TrimSuffix(strings.TrimPrefix(imports[0], "PUT /v1/datasets/tenant-a/"), "/import")
	trainCall := "POST /v1/models/tenant-a/" + job + "/train"
	var spec worker.TrainSpec
	if err := json.Unmarshal([]byte(ps.gpu.bodies[trainCall]), &spec); err != nil || spec.Dataset != job || spec.Device != DeviceGPU {
		t.Errorf("the GPU pool trained %+v (%v), want dataset %s on the gpu", spec, err, job)
	}
	importCall := "POST /v1/models/tenant-a/churn/import?dataset=customers"
	if ps.main.bodies[importCall] != `{"meta":{"from":"pool"},"onnx":"AAAA"}` {
		t.Errorf("the main worker imported %q; calls %v", ps.main.bodies[importCall], ps.main.calls)
	}
	// The pool keeps nothing.
	for _, want := range []string{"DELETE /v1/datasets/tenant-a/" + job, "DELETE /v1/models/tenant-a/" + job} {
		if !slices.Contains(ps.gpu.calls, want) {
			t.Errorf("the GPU pool was not cleaned up: no %s in %v", want, ps.gpu.calls)
		}
	}
	if len(ps.custom.calls) != 0 {
		t.Errorf("the custom pool was called: %v", ps.custom.calls)
	}
	if m, _ := ps.svc.Model(t.Context(), "tenant-a", "churn"); m.RemoteVersion != "7" {
		t.Errorf("registered version = %q, want the main worker's 7", m.RemoteVersion)
	}
}

func TestACPUTrainingStaysOnTheMainWorker(t *testing.T) {
	for _, device := range []string{"", DeviceCPU} {
		ps := newPoolSetup(t, allPools)
		_, _ = ps.svc.Train(t.Context(), "tenant-a", "churn", worker.TrainSpec{Dataset: "d", Target: "y", Device: device}, GoLive{}, "ada")
		if len(ps.gpu.calls)+len(ps.custom.calls) != 0 || len(ps.main.called("POST /v1/models/tenant-a/churn/train")) != 1 {
			t.Errorf("device %q: main %v, gpu %v, custom %v", device, ps.main.calls, ps.gpu.calls, ps.custom.calls)
		}
	}
}

func TestATrainingIsRefusedWhenItsPoolIsMissingOrOff(t *testing.T) {
	for _, tc := range []struct {
		name  string
		pools func(custom, gpu *worker.Client) Pools
		spec  worker.TrainSpec
		want  error
	}{
		{"gpu without a GPU pool", func(c, _ *worker.Client) Pools { return Pools{Custom: c, CustomScripts: true} },
			worker.TrainSpec{Device: DeviceGPU}, ErrNoGPUPool},
		{"custom scripts off", func(c, g *worker.Client) Pools { return Pools{Custom: c, GPU: g} },
			worker.TrainSpec{Algorithm: "custom:trees"}, ErrCustomScriptsOff},
		{"custom scripts on, no custom pool", func(_, g *worker.Client) Pools { return Pools{CustomScripts: true, GPU: g} },
			worker.TrainSpec{Algorithm: "custom:trees"}, ErrNoCustomPool},
		{"unknown script", allPools, worker.TrainSpec{Algorithm: "custom:nope"}, ErrScriptNotFound},
		{"unknown device", allPools, worker.TrainSpec{Device: "tpu"}, ErrBadTraining},
		{"custom script on a gpu", allPools, worker.TrainSpec{Algorithm: "custom:trees", Device: DeviceGPU}, ErrBadTraining},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ps := newPoolSetup(t, tc.pools)
			tc.spec.Dataset, tc.spec.Target = "customers", "churned"
			_, err := ps.svc.Train(t.Context(), "tenant-a", "churn", tc.spec, GoLive{}, "ada")
			if !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
			// Reading the dataset's row count (for retraining after new rows)
			// is the one call allowed; nothing is copied or trained.
			var main []string
			for _, c := range ps.main.calls {
				if c != "GET /v1/datasets/tenant-a/customers" {
					main = append(main, c)
				}
			}
			if n := len(main) + len(ps.custom.calls) + len(ps.gpu.calls); n != 0 {
				t.Errorf("%d worker calls were made: %v %v %v", n, main, ps.custom.calls, ps.gpu.calls)
			}
		})
	}
}

func TestACustomTrainingSendsTheLatestScriptToTheCustomPool(t *testing.T) {
	ps := newPoolSetup(t, allPools)
	res, err := ps.svc.Train(t.Context(), "tenant-a", "churn",
		worker.TrainSpec{Dataset: "customers", Target: "churned", Algorithm: "custom:trees"}, GoLive{}, "ada")
	if err != nil {
		t.Fatalf("Train: %v", err)
	}
	if res.Version.Version != "7" || res.Version.Log != "trained on the pool" {
		t.Errorf("result = %+v", res)
	}
	var sent struct {
		Algorithm string        `json:"algorithm"`
		Script    worker.Script `json:"script"`
	}
	calls := ps.custom.called("POST /v1/models/tenant-a/job-")
	if len(calls) != 1 || !strings.HasSuffix(calls[0], "/train-custom") {
		t.Fatalf("custom pool calls = %v", ps.custom.calls)
	}
	if err := json.Unmarshal([]byte(ps.custom.bodies[calls[0]]), &sent); err != nil {
		t.Fatal(err)
	}
	if sent.Algorithm != "custom:trees" || sent.Script.Name != "trees" || sent.Script.Source != treesScript ||
		sent.Script.SHA256 != storage.MLScriptSHA256(treesScript) {
		t.Errorf("sent %+v", sent)
	}
	if len(ps.gpu.calls) != 0 {
		t.Errorf("the GPU pool was called: %v", ps.gpu.calls)
	}
}

func TestAFailedPoolTrainingStillCleansUpThePool(t *testing.T) {
	ps := newPoolSetup(t, allPools)
	ps.custom.failTrain = true
	_, err := ps.svc.Train(t.Context(), "tenant-a", "churn",
		worker.TrainSpec{Dataset: "customers", Target: "churned", Algorithm: "custom:trees"}, GoLive{}, "ada")
	if err == nil || !strings.Contains(err.Error(), "ValueError: boom") {
		t.Fatalf("err = %v, want the script's failure", err)
	}
	if len(ps.custom.called("DELETE /v1/datasets/tenant-a/job-")) != 1 {
		t.Errorf("the custom pool kept the dataset: %v", ps.custom.calls)
	}
	if len(ps.main.called("POST")) != 0 {
		t.Errorf("a failed training reached the main worker: %v", ps.main.calls)
	}
	if _, err := ps.svc.Model(t.Context(), "tenant-a", "churn"); !errors.Is(err, ErrModelNotFound) {
		t.Errorf("a failed training registered the model: %v", err)
	}
}

func TestScriptsAreRefusedWhileCustomScriptsAreOff(t *testing.T) {
	ps := newPoolSetup(t, func(c, g *worker.Client) Pools { return Pools{Custom: c, GPU: g} })
	if _, err := ps.svc.ListScripts(t.Context(), "tenant-a"); !errors.Is(err, ErrCustomScriptsOff) {
		t.Errorf("ListScripts: err = %v", err)
	}
	if _, err := ps.svc.PutScript(t.Context(), storage.MLScript{VHost: "tenant-a", Name: "x", Source: "y"}); !errors.Is(err, ErrCustomScriptsOff) {
		t.Errorf("PutScript: err = %v", err)
	}

	on := newPoolSetup(t, allPools)
	saved, err := on.svc.PutScript(t.Context(), storage.MLScript{VHost: "tenant-a", Name: "boost", Source: "z"})
	if err != nil || saved.SHA256 != storage.MLScriptSHA256("z") {
		t.Errorf("PutScript = %+v, %v", saved, err)
	}
	if err := on.svc.DeleteScript(t.Context(), "tenant-a", "nope"); !errors.Is(err, ErrScriptNotFound) {
		t.Errorf("DeleteScript of an unknown script: err = %v", err)
	}
}

func TestPoolsFromEnv(t *testing.T) {
	t.Setenv("HERMOD_ML_CUSTOM_SCRIPTS", "true")
	t.Setenv("HERMOD_ML_CUSTOM_WORKER_URL", "http://custom:8090")
	t.Setenv("HERMOD_ML_GPU_WORKER_URL", "")
	p := PoolsFromEnv()
	if !p.CustomScripts || p.Custom == nil || p.GPU != nil {
		t.Errorf("pools = %+v", p)
	}
	t.Setenv("HERMOD_ML_CUSTOM_SCRIPTS", "yes please")
	if PoolsFromEnv().CustomScripts {
		t.Error("an unreadable HERMOD_ML_CUSTOM_SCRIPTS turned custom scripts on")
	}
}
