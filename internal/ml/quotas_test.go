package ml

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/inference"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// Runtime quota state (trainings running, the prediction buckets) is per
// replica and so per process: every test here uses vhosts of its own.

func asQuota(t *testing.T, err error, quota string) *QuotaError {
	t.Helper()
	var qe *QuotaError
	if !errors.As(err, &qe) {
		t.Fatalf("err = %v, want a QuotaError for %s", err, quota)
	}
	if qe.Quota != quota {
		t.Fatalf("refused by %s, want %s: %v", qe.Quota, quota, err)
	}
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Errorf("a QuotaError does not match ErrQuotaExceeded")
	}
	return qe
}

func TestEffectiveQuotasAreTheVHostsThenTheServerDefault(t *testing.T) {
	store := newMemStore()
	store.quotas["q-eff"] = storage.MLQuotas{VHost: "q-eff", MaxModels: new(int64(1)), MaxDatasets: new(int64(0))}
	svc := NewService(func() any { return store }, nil, nil).
		WithQuotaDefaults(storage.MLQuotas{MaxModels: new(int64(5)), MaxDatasets: new(int64(2)), MaxDatasetRows: new(int64(100))})

	q, err := svc.Quotas(t.Context(), "q-eff")
	if err != nil {
		t.Fatalf("Quotas: %v", err)
	}
	want := Quotas{MaxModels: 1, MaxDatasets: 0, MaxDatasetRows: 100}
	if q != want {
		t.Errorf("effective = %+v, want %+v (the vhost's own, 0 meaning no limit, else the default)", q, want)
	}

	other, _ := svc.Quotas(t.Context(), "q-eff-other")
	if other != (Quotas{MaxModels: 5, MaxDatasets: 2, MaxDatasetRows: 100}) {
		t.Errorf("a vhost with no quotas of its own = %+v, want the defaults", other)
	}

	none, err := NewService(func() any { return nil }, nil, nil).WithQuotaDefaults(storage.MLQuotas{}).Quotas(t.Context(), "x")
	if err != nil || none != (Quotas{}) {
		t.Errorf("a store without quotas: %+v, %v; want no limits", none, err)
	}
}

func TestQuotaDefaultsComeFromTheEnvironment(t *testing.T) {
	t.Setenv("HERMOD_ML_MAX_DATASETS", "10")
	t.Setenv("HERMOD_ML_MAX_DATASET_ROWS", "500000")
	t.Setenv("HERMOD_ML_MAX_DATASET_BYTES", "1048576")
	t.Setenv("HERMOD_ML_MAX_MODELS", "4")
	t.Setenv("HERMOD_ML_MAX_CONCURRENT_TRAININGS", "1")
	t.Setenv("HERMOD_ML_MAX_PREDICTIONS_PER_SECOND", "25.5")
	d := QuotaDefaultsFromEnv()
	if *d.MaxDatasets != 10 || *d.MaxDatasetRows != 500000 || *d.MaxDatasetBytes != 1048576 ||
		*d.MaxModels != 4 || *d.MaxConcurrentTrainings != 1 || *d.MaxPredictionsPerSecond != 25.5 {
		t.Errorf("defaults = %+v", d)
	}

	t.Setenv("HERMOD_ML_MAX_MODELS", "lots")
	t.Setenv("HERMOD_ML_MAX_DATASETS", "-3")
	t.Setenv("HERMOD_ML_MAX_DATASET_ROWS", "")
	d = QuotaDefaultsFromEnv()
	if d.MaxModels != nil || d.MaxDatasets != nil || d.MaxDatasetRows != nil {
		t.Errorf("unreadable or negative values were taken: %+v", d)
	}
}

func TestSetQuotasValidatesAndIsReadBack(t *testing.T) {
	store := newMemStore()
	svc := NewService(func() any { return store }, nil, nil).WithQuotaDefaults(storage.MLQuotas{})
	if err := svc.SetQuotas(t.Context(), storage.MLQuotas{VHost: "q-set", MaxModels: new(int64(-1))}); err == nil {
		t.Fatal("a negative quota was accepted")
	}
	if err := svc.SetQuotas(t.Context(), storage.MLQuotas{VHost: "q-set", MaxModels: new(int64(2)), UpdatedBy: "root"}); err != nil {
		t.Fatalf("SetQuotas: %v", err)
	}
	stored, err := svc.StoredQuotas(t.Context(), "q-set")
	if err != nil || stored.MaxModels == nil || *stored.MaxModels != 2 {
		t.Errorf("stored = %+v, %v", stored, err)
	}
	if empty, err := svc.StoredQuotas(t.Context(), "q-set-none"); err != nil || empty.VHost != "q-set-none" || empty.MaxModels != nil {
		t.Errorf("a vhost with none set = %+v, %v; want an empty set", empty, err)
	}
	if err := NewService(func() any { return nil }, nil, nil).SetQuotas(t.Context(), storage.MLQuotas{VHost: "x"}); !errors.Is(err, storage.ErrMLQuotasUnsupported) {
		t.Errorf("a store without quotas: err = %v", err)
	}
}

func TestPutModelRefusesANewModelOverTheQuota(t *testing.T) {
	existing := storage.MLModel{VHost: "q-models", Name: "a", Backend: inference.BackendMLflow, URL: "http://x"}
	store := newMemStore(existing)
	store.quotas["q-models"] = storage.MLQuotas{VHost: "q-models", MaxModels: new(int64(1))}
	svc := NewService(func() any { return store }, nil, nil).WithQuotaDefaults(storage.MLQuotas{})
	before := testutil.ToFloat64(quotaRefusals.WithLabelValues("q-models", QuotaModels))

	err := svc.PutModel(t.Context(), storage.MLModel{VHost: "q-models", Name: "b", Backend: inference.BackendMLflow, URL: "http://x"})
	qe := asQuota(t, err, QuotaModels)
	if qe.Retryable || !strings.Contains(err.Error(), "1 model") || !strings.Contains(err.Error(), "q-models") {
		t.Errorf("refusal = %q (retryable %v), want one that names the vhost and the limit", err, qe.Retryable)
	}
	if _, err := svc.Model(t.Context(), "q-models", "b"); !errors.Is(err, ErrModelNotFound) {
		t.Error("the refused model was saved")
	}
	if got := testutil.ToFloat64(quotaRefusals.WithLabelValues("q-models", QuotaModels)); got != before+1 {
		t.Errorf("refusals metric moved by %v, want 1", got-before)
	}

	existing.Description = "edited"
	if err := svc.PutModel(t.Context(), existing); err != nil {
		t.Errorf("editing a model the vhost already has was refused: %v", err)
	}
}

func TestTrainingANewModelOverTheModelQuotaIsRefusedBeforeTheWorkerIsCalled(t *testing.T) {
	store := newMemStore(storage.MLModel{VHost: "q-train", Name: "churn", Backend: storage.MLBackendWorker, RemoteVersion: "1"})
	store.quotas["q-train"] = storage.MLQuotas{VHost: "q-train", MaxModels: new(int64(1))}
	svc, f := workerService(t, store, 0.9)
	svc.WithQuotaDefaults(storage.MLQuotas{})

	_, err := svc.Train(t.Context(), "q-train", "fraud", worker.TrainSpec{Dataset: "d", Target: "y"}, GoLive{Mode: GoLiveAlways}, "ada")
	asQuota(t, err, QuotaModels)
	if len(f.calls) != 0 {
		t.Errorf("the worker was asked to train a refused model: %v", f.calls)
	}
	if _, err := svc.Train(t.Context(), "q-train", "churn", worker.TrainSpec{Dataset: "d", Target: "y"}, GoLive{Mode: GoLiveAlways}, "ada"); err != nil {
		t.Errorf("retraining a model the vhost already has was refused: %v", err)
	}
}

// slowWorker trains only when told to, so a test can hold trainings open.
type slowWorker struct {
	started chan struct{}
	release chan struct{}
}

func (s *slowWorker) start(t *testing.T) *worker.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read to the end, so the server notices a client that goes away.
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case s.started <- struct{}{}:
		case <-r.Context().Done():
			return
		}
		select {
		case <-s.release:
		case <-r.Context().Done():
			return
		}
		_ = json.NewEncoder(w).Encode(worker.Version{Version: "1", Features: []string{"a"}, Metrics: map[string]float64{"score": 1}})
	}))
	t.Cleanup(srv.Close)
	return worker.New(srv.URL, "", nil)
}

func TestConcurrentTrainingsOfAVHostAreLimited(t *testing.T) {
	store := newMemStore()
	store.quotas["q-conc"] = storage.MLQuotas{VHost: "q-conc", MaxConcurrentTrainings: new(int64(1))}
	sw := &slowWorker{started: make(chan struct{}, 4), release: make(chan struct{})}
	svc := NewService(func() any { return store }, nil, nil).WithWorker(sw.start(t)).WithQuotaDefaults(storage.MLQuotas{})
	spec := worker.TrainSpec{Dataset: "d", Target: "y"}

	first := make(chan error, 1)
	go func() {
		_, err := svc.Train(t.Context(), "q-conc", "one", spec, GoLive{Mode: GoLiveAlways}, "ada")
		first <- err
	}()
	<-sw.started

	// Bounded, so a missing limit fails the test rather than hanging it.
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, err := svc.Train(ctx, "q-conc", "two", spec, GoLive{Mode: GoLiveAlways}, "ada")
	if qe := asQuota(t, err, QuotaConcurrentTrainings); !qe.Retryable {
		t.Error("a busy-training refusal is not marked retryable")
	}
	// Another vhost is not held up by this one.
	store.quotas["q-conc-b"] = storage.MLQuotas{VHost: "q-conc-b", MaxConcurrentTrainings: new(int64(1))}
	second := make(chan error, 1)
	go func() {
		_, err := svc.Train(t.Context(), "q-conc-b", "one", spec, GoLive{Mode: GoLiveAlways}, "ada")
		second <- err
	}()
	<-sw.started

	sw.release <- struct{}{}
	sw.release <- struct{}{}
	if err := <-first; err != nil {
		t.Fatalf("the first training: %v", err)
	}
	if err := <-second; err != nil {
		t.Fatalf("the other vhost's training: %v", err)
	}

	// The slot is given back when a training finishes.
	go func() { sw.release <- struct{}{} }()
	if _, err := svc.Train(t.Context(), "q-conc", "two", spec, GoLive{Mode: GoLiveAlways}, "ada"); err != nil {
		t.Errorf("a training after the first finished was refused: %v", err)
	}
	<-sw.started
}

func TestPredictionsPerSecondAreATokenBucketPerVHost(t *testing.T) {
	var auth string
	srv := mlflowEcho(t, &auth)
	store := newMemStore(
		storage.MLModel{VHost: "q-rate", Name: "double", Backend: inference.BackendMLflow, URL: srv.URL},
		storage.MLModel{VHost: "q-rate-b", Name: "double", Backend: inference.BackendMLflow, URL: srv.URL},
	)
	store.quotas["q-rate"] = storage.MLQuotas{VHost: "q-rate", MaxPredictionsPerSecond: new(2.0)}
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	svc := NewService(func() any { return store }, nil, nil).WithQuotaDefaults(storage.MLQuotas{})
	svc.now = func() time.Time { return now }
	row := []inference.Row{{"x": 1.0}}

	for i := range 2 {
		if _, err := svc.Predict(t.Context(), "q-rate", "double", row); err != nil {
			t.Fatalf("prediction %d within the rate: %v", i+1, err)
		}
	}
	_, err := svc.Predict(t.Context(), "q-rate", "double", row)
	if qe := asQuota(t, err, QuotaPredictionsPerSecond); !qe.Retryable {
		t.Error("a rate refusal is not marked retryable")
	}
	if _, err := svc.Predict(t.Context(), "q-rate-b", "double", row); err != nil {
		t.Errorf("another vhost was held to this vhost's rate: %v", err)
	}

	now = now.Add(time.Second)
	if _, err := svc.Predict(t.Context(), "q-rate", "double", []inference.Row{{"x": 1.0}, {"x": 2.0}}); err != nil {
		t.Errorf("a second later, two rows were refused: %v", err)
	}

	now = now.Add(time.Minute)
	_, err = svc.Predict(t.Context(), "q-rate", "double", []inference.Row{{"x": 1.0}, {"x": 2.0}, {"x": 3.0}})
	asQuota(t, err, QuotaPredictionsPerSecond)
	if !strings.Contains(err.Error(), "split") {
		t.Errorf("a call larger than a second's worth does not say to split it: %v", err)
	}
}

// datasetWorker is a worker's dataset routes, in memory.
type datasetWorker struct {
	mu       sync.Mutex
	datasets map[string]int // name -> rows
	// uploadRows is how many rows an uploaded file turns out to hold.
	uploadRows int
	calls      []string
	appended   int
}

func (d *datasetWorker) start(t *testing.T, vhost string) *worker.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		defer d.mu.Unlock()
		d.calls = append(d.calls, r.Method+" "+r.URL.Path)
		base := "/v1/datasets/" + vhost
		name := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, base), "/")
		name, suffix, _ := strings.Cut(name, "/")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == base:
			var list []worker.DatasetInfo
			for n, rows := range d.datasets {
				list = append(list, worker.DatasetInfo{Name: n, Rows: rows})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"datasets": list})
		case r.Method == http.MethodPut && suffix == "file":
			if _, err := io.ReadAll(r.Body); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			d.datasets[name] = d.uploadRows
			_ = json.NewEncoder(w).Encode(worker.DatasetInfo{Name: name, Rows: d.uploadRows})
		case r.Method == http.MethodPost && suffix == "rows":
			var body struct {
				Rows    []map[string]any `json:"rows"`
				Replace bool             `json:"replace"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Replace {
				d.datasets[name] = 0
			}
			d.datasets[name] += len(body.Rows)
			d.appended += len(body.Rows)
			_ = json.NewEncoder(w).Encode(map[string]any{"rows": d.datasets[name]})
		case r.Method == http.MethodDelete:
			delete(d.datasets, name)
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return worker.New(srv.URL, "", nil)
}

func (d *datasetWorker) did(call string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, c := range d.calls {
		if c == call {
			return true
		}
	}
	return false
}

func datasetService(t *testing.T, vhost string, q storage.MLQuotas, existing map[string]int) (*Service, *datasetWorker) {
	t.Helper()
	store := newMemStore()
	q.VHost = vhost
	store.quotas[vhost] = q
	dw := &datasetWorker{datasets: existing, uploadRows: 2}
	if dw.datasets == nil {
		dw.datasets = map[string]int{}
	}
	return NewService(func() any { return store }, nil, nil).WithWorker(dw.start(t, vhost)).WithQuotaDefaults(storage.MLQuotas{}), dw
}

func TestUploadingANewDatasetOverTheDatasetQuotaIsRefused(t *testing.T) {
	svc, dw := datasetService(t, "q-ds", storage.MLQuotas{MaxDatasets: new(int64(2))}, map[string]int{"a": 1, "b": 1})

	_, err := svc.UploadDataset(t.Context(), "q-ds", "c", "csv", strings.NewReader("x\n1\n"))
	asQuota(t, err, QuotaDatasets)
	if dw.did("PUT /v1/datasets/q-ds/c/file") {
		t.Error("the refused file was sent to the worker")
	}
	if _, err := svc.UploadDataset(t.Context(), "q-ds", "a", "csv", strings.NewReader("x\n1\n")); err != nil {
		t.Errorf("replacing a dataset the vhost already has was refused: %v", err)
	}
}

func TestUploadingMoreBytesThanTheQuotaIsRefused(t *testing.T) {
	svc, _ := datasetService(t, "q-bytes", storage.MLQuotas{MaxDatasetBytes: new(int64(8))}, nil)
	_, err := svc.UploadDataset(t.Context(), "q-bytes", "d", "csv", strings.NewReader("x\n1\n2\n3\n4\n5\n"))
	asQuota(t, err, QuotaDatasetBytes)
	if _, err := svc.UploadDataset(t.Context(), "q-bytes", "d", "csv", strings.NewReader("x\n1\n")); err != nil {
		t.Errorf("a file within the byte quota was refused: %v", err)
	}
}

func TestAnUploadHoldingMoreRowsThanTheQuotaIsRemoved(t *testing.T) {
	svc, dw := datasetService(t, "q-rows", storage.MLQuotas{MaxDatasetRows: new(int64(3))}, nil)
	dw.uploadRows = 5
	_, err := svc.UploadDataset(t.Context(), "q-rows", "d", "csv", strings.NewReader("x\n1\n2\n3\n4\n5\n"))
	asQuota(t, err, QuotaDatasetRows)
	if !dw.did("DELETE /v1/datasets/q-rows/d") {
		t.Error("the dataset over the row quota was left on the worker")
	}
}

func TestDatasetFromQueryIsHeldToTheVHostsQuotas(t *testing.T) {
	db := ordersDB(t, 30)
	const q = "SELECT id, amount FROM orders"

	t.Run("rows", func(t *testing.T) {
		svc, _ := datasetService(t, "q-qrows", storage.MLQuotas{MaxDatasetRows: new(int64(10))}, nil)
		_, err := svc.DatasetFromQuery(t.Context(), "q-qrows", "orders", db, q, 0)
		asQuota(t, err, QuotaDatasetRows)
		// A cap the caller names below the quota is still the caller's cap.
		_, err = svc.DatasetFromQuery(t.Context(), "q-qrows", "orders", db, q, 5)
		var qe *QuotaError
		if err == nil || errors.As(err, &qe) {
			t.Errorf("the caller's own cap of 5: err = %v, want a plain refusal", err)
		}
		if _, err := svc.DatasetFromQuery(t.Context(), "q-qrows", "orders", db, "SELECT id FROM orders LIMIT 10", 0); err != nil {
			t.Errorf("10 rows under a quota of 10 were refused: %v", err)
		}
	})
	t.Run("bytes", func(t *testing.T) {
		svc, dw := datasetService(t, "q-qbytes", storage.MLQuotas{MaxDatasetBytes: new(int64(100))}, nil)
		_, err := svc.DatasetFromQuery(t.Context(), "q-qbytes", "orders", db, q, 0)
		asQuota(t, err, QuotaDatasetBytes)
		if dw.appended > 30 {
			t.Errorf("%d rows reached the worker", dw.appended)
		}
	})
	t.Run("datasets", func(t *testing.T) {
		svc, dw := datasetService(t, "q-qds", storage.MLQuotas{MaxDatasets: new(int64(1))}, map[string]int{"other": 3})
		_, err := svc.DatasetFromQuery(t.Context(), "q-qds", "orders", db, q, 0)
		asQuota(t, err, QuotaDatasets)
		if dw.appended != 0 {
			t.Errorf("%d rows of a refused dataset reached the worker", dw.appended)
		}
	})
}
