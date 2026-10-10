package ml

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/ml/monitor"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/inference"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// predLogs is a prediction log store in memory.
type predLogs struct {
	mu      sync.Mutex
	rows    []storage.MLPredictionLog
	deleted []string
}

func (p *predLogs) InsertMLPredictionLogs(_ context.Context, logs []storage.MLPredictionLog) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.rows = append(p.rows, logs...)
	return nil
}
func (p *predLogs) ListMLPredictionLogs(_ context.Context, vhost, model string, limit int) ([]storage.MLPredictionLog, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []storage.MLPredictionLog
	for i := len(p.rows) - 1; i >= 0 && len(out) < limit; i-- {
		if p.rows[i].VHost == vhost && p.rows[i].Model == model {
			out = append(out, p.rows[i])
		}
	}
	return out, nil
}
func (p *predLogs) PurgeMLPredictionLogs(context.Context, string, string, time.Time) error {
	return nil
}
func (p *predLogs) DeleteMLPredictionLogs(_ context.Context, vhost, model string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.deleted = append(p.deleted, vhost+"/"+model)
	return nil
}

func (p *predLogs) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.rows)
}

// startMonitor runs a monitor that writes and judges at once.
func startMonitor(t *testing.T, svc *Service, logs any) {
	t.Helper()
	mon := monitor.New(monitor.Config{Window: 20 * time.Millisecond, MinRows: 1, Flush: 10 * time.Millisecond},
		monitor.Deps{Logs: func() any { return logs }, Stats: svc.VersionStats})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		mon.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	svc.WithMonitor(mon)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestPredictLogsTheCallWithItsCallerAndNotAFailedOne(t *testing.T) {
	var auth string
	good := mlflowEcho(t, &auth)
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) }))
	t.Cleanup(bad.Close)
	logged := storage.MLMonitoring{LogSampleRate: 1}
	store := newMemStore(
		storage.MLModel{VHost: "a", Name: "double", Backend: inference.BackendMLflow, URL: good.URL, Monitoring: logged},
		storage.MLModel{VHost: "a", Name: "broken", Backend: inference.BackendMLflow, URL: bad.URL, Monitoring: logged},
	)
	logs := &predLogs{}
	svc := NewService(func() any { return store }, nil, nil).WithLogStore(func() any { return logs })
	startMonitor(t, svc, logs)

	if _, err := svc.Predict(WithCaller(t.Context(), storage.MLCallerWorkflow, "wf-9"), "a", "broken", []inference.Row{{"x": 1.0}}); err == nil {
		t.Fatal("the broken model answered")
	}
	if _, err := svc.Predict(WithCaller(t.Context(), storage.MLCallerREST, ""), "a", "double", []inference.Row{{"x": 2.0}}); err != nil {
		t.Fatalf("Predict: %v", err)
	}
	waitFor(t, "the prediction to be logged", func() bool { return logs.count() == 1 })

	got, err := svc.PredictionLogs(t.Context(), "a", "double", 10)
	if err != nil || len(got) != 1 {
		t.Fatalf("PredictionLogs = %+v, %v", got, err)
	}
	if got[0].CallerKind != storage.MLCallerREST || got[0].Inputs["x"] != 2.0 || got[0].Outputs["prediction"] != 4.0 {
		t.Errorf("logged %+v", got[0])
	}
	if broken, _ := svc.PredictionLogs(t.Context(), "a", "broken", 10); len(broken) != 0 {
		t.Errorf("a failed call was logged: %+v", broken)
	}
}

func TestPredictWithNoCallerNamedIsLoggedAsUnknown(t *testing.T) {
	var auth string
	srv := mlflowEcho(t, &auth)
	store := newMemStore(storage.MLModel{VHost: "a", Name: "double", Backend: inference.BackendMLflow, URL: srv.URL,
		Monitoring: storage.MLMonitoring{LogSampleRate: 1}})
	logs := &predLogs{}
	svc := NewService(func() any { return store }, nil, nil).WithLogStore(func() any { return logs })
	startMonitor(t, svc, logs)
	if _, err := svc.Predict(t.Context(), "a", "double", []inference.Row{{"x": 1.0}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the prediction to be logged", func() bool { return logs.count() == 1 })
	if got, _ := svc.PredictionLogs(t.Context(), "a", "double", 1); got[0].CallerKind != "" {
		t.Errorf("caller = %q, want none", got[0].CallerKind)
	}
}

func liveChurn() storage.MLModel {
	return storage.MLModel{VHost: "tenant-a", Name: "churn", Backend: storage.MLBackendWorker, RemoteVersion: "2", Features: []string{"age"}}
}

func ageStats() map[string]worker.FeatureStats {
	return map[string]worker.FeatureStats{"age": {Kind: worker.StatsNumeric, Count: 100,
		Edges: []float64{30, 50}, Fractions: []float64{0.3, 0.4, 0.3}}}
}

func TestATrainedModelsDriftIsMeasuredFromItsPredictions(t *testing.T) {
	store := newMemStore(liveChurn())
	svc, f := workerService(t, store, 0)
	f.versions["tenant-a/churn"] = []worker.Version{{Model: "churn", Version: "2", Features: []string{"age"}, FeatureStats: ageStats()}}
	startMonitor(t, svc, &predLogs{})

	before, err := svc.Drift(t.Context(), "tenant-a", "churn")
	if err != nil || before.Report != nil || before.Reason == "" || before.MinRows != 1 {
		t.Fatalf("before any prediction: %+v, %v", before, err)
	}
	if _, err := svc.Predict(t.Context(), "tenant-a", "churn", []inference.Row{{"age": 90.0}, {"age": 95.0}}); err != nil {
		t.Fatalf("Predict: %v", err)
	}
	var got DriftStatus
	waitFor(t, "a drift report", func() bool {
		got, _ = svc.Drift(t.Context(), "tenant-a", "churn")
		return got.Report != nil
	})
	r := got.Report
	if r.Version != "2" || r.Rows != 2 || r.Status != monitor.StatusAlert || r.Features[0].Feature != "age" {
		t.Errorf("report = %+v", r)
	}
}

func TestDriftSaysWhyThereIsNoReport(t *testing.T) {
	external := storage.MLModel{VHost: "a", Name: "fraud", Backend: inference.BackendMLflow, URL: "http://x"}
	store := newMemStore(external, liveChurn())
	svc, _ := workerService(t, store, 0)

	off, err := svc.Drift(t.Context(), "tenant-a", "churn")
	if err != nil || off.Report != nil || off.Reason == "" {
		t.Errorf("without a monitor: %+v, %v", off, err)
	}
	startMonitor(t, svc, &predLogs{})
	ext, err := svc.Drift(t.Context(), "a", "fraud")
	if err != nil || ext.Report != nil || ext.Reason == "" || ext.Reason == off.Reason {
		t.Errorf("a model served elsewhere: %+v, %v", ext, err)
	}
	if _, err := svc.Drift(t.Context(), "a", "nope"); !errors.Is(err, ErrModelNotFound) {
		t.Errorf("an unknown model: err = %v", err)
	}
}

func TestPredictionLogsNeedTheModelAndALogStore(t *testing.T) {
	store := newMemStore(storage.MLModel{VHost: "a", Name: "m", Backend: inference.BackendMLflow, URL: "http://x"})
	svc := NewService(func() any { return store }, nil, nil).WithLogStore(func() any { return struct{}{} })
	if _, err := svc.PredictionLogs(t.Context(), "a", "m", 10); !errors.Is(err, storage.ErrMLPredictionLogsUnsupported) {
		t.Errorf("a log store without prediction logs: err = %v", err)
	}
	svc.WithLogStore(func() any { return &predLogs{} })
	if _, err := svc.PredictionLogs(t.Context(), "b", "m", 10); !errors.Is(err, ErrModelNotFound) {
		t.Errorf("another vhost's model: err = %v", err)
	}
	if got, err := svc.PredictionLogs(t.Context(), "a", "m", 10); err != nil || got == nil {
		t.Errorf("an empty log = %#v, %v; want an empty list", got, err)
	}
}

func TestSetMonitoringValidatesAndKeepsTheDefinition(t *testing.T) {
	store := newMemStore(storage.MLModel{VHost: "a", Name: "m", Backend: inference.BackendMLflow, URL: "http://x", Features: []string{"x"}})
	svc := NewService(func() any { return store }, nil, nil)

	if _, err := svc.SetMonitoring(t.Context(), "a", "m", storage.MLMonitoring{LogSampleRate: 2}, "ada"); err == nil {
		t.Error("a sample rate of 2 was accepted")
	}
	got, err := svc.SetMonitoring(t.Context(), "a", "m", storage.MLMonitoring{LogSampleRate: 0.1, DriftAlert: 0.5}, "ada")
	if err != nil {
		t.Fatalf("SetMonitoring: %v", err)
	}
	saved, _ := store.GetMLModel(t.Context(), "a", "m")
	if saved.Monitoring.LogSampleRate != 0.1 || saved.Monitoring.DriftAlert != 0.5 || saved.URL != "http://x" ||
		len(saved.Features) != 1 || saved.UpdatedBy != "ada" || got.Monitoring.LogSampleRate != 0.1 {
		t.Errorf("saved = %+v", saved)
	}
	if _, err := svc.SetMonitoring(t.Context(), "a", "nope", storage.MLMonitoring{}, "ada"); !errors.Is(err, ErrModelNotFound) {
		t.Errorf("an unknown model: err = %v", err)
	}
}

func TestVersionStatsAreThoseOfTheVersionAsked(t *testing.T) {
	store := newMemStore(liveChurn())
	svc, f := workerService(t, store, 0)
	f.versions["tenant-a/churn"] = []worker.Version{
		{Model: "churn", Version: "3"},
		{Model: "churn", Version: "2", FeatureStats: ageStats()},
	}
	got, err := svc.VersionStats(t.Context(), "tenant-a", "churn", "2")
	if err != nil || got["age"].Kind != worker.StatsNumeric {
		t.Errorf("version 2: %+v, %v", got, err)
	}
	if got, err := svc.VersionStats(t.Context(), "tenant-a", "churn", "3"); err != nil || len(got) != 0 {
		t.Errorf("a version without stats: %+v, %v", got, err)
	}
	if _, err := svc.VersionStats(t.Context(), "tenant-a", "churn", "9"); !errors.Is(err, ErrVersionNotFound) {
		t.Errorf("an unknown version: err = %v", err)
	}
}

func TestDeletingAModelDeletesItsPredictionLogs(t *testing.T) {
	store := newMemStore(storage.MLModel{VHost: "a", Name: "m", Backend: inference.BackendMLflow, URL: "http://x"})
	logs := &predLogs{}
	svc := NewService(func() any { return store }, nil, nil).WithLogStore(func() any { return logs })
	if err := svc.DeleteModel(t.Context(), "a", "m"); err != nil {
		t.Fatal(err)
	}
	if len(logs.deleted) != 1 || logs.deleted[0] != "a/m" {
		t.Errorf("deleted logs of %v, want a/m", logs.deleted)
	}
}
