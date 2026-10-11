package ml

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/inference"
	"github.com/gsoultan/hermod/pkg/ml/recommend"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// recFixtures is where pkg/ml/recommend keeps the recommenders the worker
// trained for its parity tests, with what the worker answered for them.
const recFixtures = "../../pkg/ml/recommend/testdata"

// recWorker is a hermod-ml worker holding version 1 of recommendation model
// "recs" in vhost "v": one parity fixture's artifact. It counts the fetches.
type recWorker struct {
	artifact []byte
	files    atomic.Int32
}

func newRecWorker(t *testing.T, algorithm string) (*recWorker, *worker.Client) {
	t.Helper()
	w := &recWorker{}
	var err error
	if w.artifact, err = os.ReadFile(filepath.Join(recFixtures, algorithm, "model.rec")); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/models/v/recs/versions/1/model.rec" {
			w.files.Add(1)
			_, _ = rw.Write(w.artifact)
			return
		}
		http.NotFound(rw, r)
	}))
	t.Cleanup(srv.Close)
	return w, worker.New(srv.URL, "", nil)
}

func trainedRecs() storage.MLModel {
	return storage.MLModel{VHost: "v", Name: "recs", Backend: storage.MLBackendWorker, RemoteVersion: "1",
		Task: storage.MLTaskRecommendation}
}

// recCases reads a fixture's calls and what the worker answered.
func recCases(t *testing.T, algorithm string) []struct {
	User, Item  string
	K           int
	ExcludeSeen bool `json:"exclude_seen"`
	Items       []recommend.Item
} {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(recFixtures, algorithm, "cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		User, Item  string
		K           int
		ExcludeSeen bool `json:"exclude_seen"`
		Items       []recommend.Item
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	return cases
}

func sameItems(got, want []recommend.Item) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		d := got[i].Score - want[i].Score
		if got[i].Item != want[i].Item || got[i].Reason != want[i].Reason || d > 1e-5 || d < -1e-5 {
			return false
		}
	}
	return true
}

// TestARecommenderIsServedFromItsStoredModel starts where an operator does —
// a stored recommendation model with version 1 live — and goes through
// Recommend: the answers are the worker's own, and the artifact is fetched
// once for every call, not per call.
func TestARecommenderIsServedFromItsStoredModel(t *testing.T) {
	resetRecommenders()
	for _, algorithm := range []string{"als", "item_similarity"} {
		resetRecommenders()
		w, client := newRecWorker(t, algorithm)
		svc := NewService(func() any { return newMemStore(trainedRecs()) }, nil, nil).WithWorker(client).
			WithQuotaDefaults(storage.MLQuotas{})
		for _, c := range recCases(t, algorithm) {
			req := RecommendRequest{K: c.K}
			if c.User != "" {
				req.User = c.User
				req.ExcludeSeen = new(c.ExcludeSeen)
			} else {
				req.Item = c.Item
			}
			got, err := svc.Recommend(t.Context(), "v", "recs", req)
			if err != nil {
				t.Fatalf("%s Recommend(%+v): %v", algorithm, req, err)
			}
			if !sameItems(got, c.Items) {
				t.Errorf("%s Recommend(%+v) = %+v, the worker said %+v", algorithm, req, got, c.Items)
			}
		}
		if n := w.files.Load(); n != 1 {
			t.Errorf("%s: the artifact was fetched %d times, want once", algorithm, n)
		}
	}
}

// A person whose id was a number in the dataset is found from a JSON number,
// and exclude_seen defaults to true.
func TestRecommendDefaultsAndNumericIDs(t *testing.T) {
	resetRecommenders()
	_, client := newRecWorker(t, "item_similarity")
	svc := NewService(func() any { return newMemStore(trainedRecs()) }, nil, nil).WithWorker(client).
		WithQuotaDefaults(storage.MLQuotas{})
	want := recCases(t, "item_similarity")[0] // user "1000", k 10, exclude_seen
	got, err := svc.Recommend(t.Context(), "v", "recs", RecommendRequest{User: 1000.0})
	if err != nil || !sameItems(got, want.Items) {
		t.Fatalf("Recommend(1000) = %+v, %v; want %+v", got, err, want.Items)
	}
	big, err := svc.Recommend(t.Context(), "v", "recs", RecommendRequest{User: "1000", K: 5000})
	if err != nil || len(big) > recommend.MaxK {
		t.Errorf("k=5000 answered %d items, %v; want at most %d", len(big), err, recommend.MaxK)
	}
}

func TestRecommendRequestsAreChecked(t *testing.T) {
	resetRecommenders()
	_, client := newRecWorker(t, "als")
	svc := NewService(func() any { return newMemStore(trainedRecs()) }, nil, nil).WithWorker(client).
		WithQuotaDefaults(storage.MLQuotas{})
	cases := map[string]RecommendRequest{
		"neither":                {K: 3},
		"both":                   {User: "1000", Item: "sku-1"},
		"negative k":             {User: "1000", K: -1},
		"exclude_seen with item": {Item: "sku-1", ExcludeSeen: new(true)},
		"object id":              {User: map[string]any{"id": 1}},
		"empty id":               {Item: ""},
	}
	for name, req := range cases {
		if _, err := svc.Recommend(t.Context(), "v", "recs", req); !errors.Is(err, ErrBadRecommendRequest) {
			t.Errorf("%s: err = %v, want ErrBadRecommendRequest", name, err)
		}
	}
}

// Rows go to a model that predicts a column, a person or an item to a
// recommender; each refuses the other's call, before anything is sent.
func TestPredictAndRecommendRefuseTheOtherKindOfModel(t *testing.T) {
	resetRecommenders()
	w, client := newRecWorker(t, "als")
	store := newMemStore(trainedRecs(), trainedChurn(""))
	svc := NewService(func() any { return store }, nil, nil).WithWorker(client).WithQuotaDefaults(storage.MLQuotas{})
	if _, err := svc.Predict(t.Context(), "v", "recs", []inference.Row{{"user": "1000"}}); !errors.Is(err, ErrRecommender) {
		t.Errorf("Predict on a recommender = %v, want ErrRecommender", err)
	}
	if _, err := svc.Recommend(t.Context(), "v", "churn", RecommendRequest{User: "1000"}); !errors.Is(err, ErrNotRecommender) {
		t.Errorf("Recommend on a predictor = %v, want ErrNotRecommender", err)
	}
	if _, err := svc.Recommend(t.Context(), "v", "nothing", RecommendRequest{User: "1000"}); !errors.Is(err, ErrModelNotFound) {
		t.Errorf("Recommend on no model = %v, want ErrModelNotFound", err)
	}
	if w.files.Load() != 0 {
		t.Errorf("the artifact was fetched for a refused call")
	}
}

func TestRecommendationsCountAgainstThePredictionRate(t *testing.T) {
	resetRecommenders()
	_, client := newRecWorker(t, "als")
	m := trainedRecs()
	m.VHost = "q-rec"
	store := newMemStore(m)
	store.quotas["q-rec"] = storage.MLQuotas{VHost: "q-rec", MaxPredictionsPerSecond: new(1.0)}
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	svc := NewService(func() any { return store }, nil, nil).WithWorker(client).WithQuotaDefaults(storage.MLQuotas{})
	svc.now = func() time.Time { return now }
	// The fixture lives under vhost v on the fake worker; the quota is
	// checked before the artifact is asked for, so the vhost does not matter.
	_, _ = svc.Recommend(t.Context(), "q-rec", "recs", RecommendRequest{User: "1000"})
	_, err := svc.Recommend(t.Context(), "q-rec", "recs", RecommendRequest{User: "1000"})
	if qe := asQuota(t, err, QuotaPredictionsPerSecond); !qe.Retryable {
		t.Error("a rate refusal is not marked retryable")
	}
}

// An artifact Hermod cannot parse is an error, remembered until the version
// changes: it is not fetched again on every call.
func TestABadArtifactIsRefusedAndRemembered(t *testing.T) {
	resetRecommenders()
	w, client := newRecWorker(t, "als")
	w.artifact = []byte("HREC\x01\x00\x00\x00\x02\x00\x00\x00{}")
	svc := NewService(func() any { return newMemStore(trainedRecs()) }, nil, nil).WithWorker(client).
		WithQuotaDefaults(storage.MLQuotas{})
	for range 3 {
		if _, err := svc.Recommend(t.Context(), "v", "recs", RecommendRequest{User: "1000"}); !errors.Is(err, recommend.ErrInvalid) {
			t.Fatalf("Recommend = %v, want ErrInvalid", err)
		}
	}
	if w.files.Load() != 1 {
		t.Errorf("the bad artifact was fetched %d times, want once", w.files.Load())
	}
}

func TestTrainingARecommenderKeepsItsTaskAndRunsOnTheMainWorker(t *testing.T) {
	store := newMemStore()
	svc, _ := workerService(t, store, 0.4)
	spec := worker.TrainSpec{Dataset: "orders", Task: worker.TaskRecommendation,
		Recommend: &worker.RecommendSpec{User: "customer", Item: "sku"}}
	res, err := svc.Train(t.Context(), "v", "recs", spec, GoLive{Mode: GoLiveAlways}, "ada")
	if err != nil {
		t.Fatalf("Train: %v", err)
	}
	m, err := svc.Model(t.Context(), "v", "recs")
	if err != nil || !m.IsRecommender() || !res.Model.IsRecommender() {
		t.Fatalf("registered %+v, %v; want a recommender", m, err)
	}
	if m.Description != "Recommends sku to each customer, from dataset orders" {
		t.Errorf("description = %q", m.Description)
	}

	for name, bad := range map[string]worker.TrainSpec{
		"gpu":                      {Dataset: "orders", Task: worker.TaskRecommendation, Recommend: spec.Recommend, Device: DeviceGPU},
		"script":                   {Dataset: "orders", Task: worker.TaskRecommendation, Recommend: spec.Recommend, Algorithm: CustomPrefix + "mine"},
		"no columns":               {Dataset: "orders", Task: worker.TaskRecommendation},
		"columns without the task": {Dataset: "orders", Target: "sku", Recommend: spec.Recommend},
	} {
		if _, err := svc.Train(t.Context(), "v", "recs", bad, GoLive{}, "ada"); !errors.Is(err, ErrBadTraining) {
			t.Errorf("%s: Train = %v, want ErrBadTraining", name, err)
		}
	}
}

// resetRecommenders empties the recommender cache, so each test loads its own.
func resetRecommenders() {
	recommenders.mu.Lock()
	defer recommenders.mu.Unlock()
	recommenders.entries = map[string]*versionEntry[*recommend.Model]{}
}

// A recommendation call is logged like a prediction, and watched by coverage
// and popular share rather than feature drift.
func TestRecommendationsAreLoggedAndMonitoredInsteadOfDrift(t *testing.T) {
	resetRecommenders()
	_, client := newRecWorker(t, "item_similarity")
	m := trainedRecs()
	m.Monitoring = storage.MLMonitoring{LogSampleRate: 1}
	store, logs := newMemStore(m), &predLogs{}
	svc := NewService(func() any { return store }, nil, nil).WithWorker(client).WithQuotaDefaults(storage.MLQuotas{}).
		WithLogStore(func() any { return logs })
	startMonitor(t, svc, logs)

	ctx := WithCaller(t.Context(), storage.MLCallerREST, "")
	if _, err := svc.Recommend(ctx, "v", "recs", RecommendRequest{User: "1000", K: 3}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Recommend(ctx, "v", "recs", RecommendRequest{User: "stranger", K: 3}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "two logged recommendations", func() bool { return logs.count() == 2 })
	logs.mu.Lock()
	first := logs.rows[0]
	logs.mu.Unlock()
	if first.Inputs["user"] != "1000" || first.Inputs["k"] != 3 || first.CallerKind != storage.MLCallerREST {
		t.Errorf("logged inputs = %v by %q", first.Inputs, first.CallerKind)
	}
	if items, _ := first.Outputs["items"].([]any); len(items) != 3 {
		t.Errorf("logged outputs = %v, want three items", first.Outputs)
	}

	var st DriftStatus
	waitFor(t, "a recommendation report", func() bool {
		var err error
		st, err = svc.Drift(t.Context(), "v", "recs")
		return err == nil && st.Recommendation != nil
	})
	r := st.Recommendation
	if st.Report != nil || r.Calls != 2 || r.Fallbacks != 1 || r.PopularShare != 0.5 || r.Catalog != 60 ||
		r.Recommended < 3 || r.Coverage != float64(r.Recommended)/60 {
		t.Errorf("drift status = %+v, report %+v", st, r)
	}
}
