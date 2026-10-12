package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/ml"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// The feedback and schedules of modelAPIStore, in memory.
var (
	apiFeedback  = map[*modelAPIStore][]storage.MLFeedbackEvent{}
	apiSchedules = map[*modelAPIStore]map[string]storage.MLSchedule{}
)

func (s *modelAPIStore) AddMLFeedback(_ context.Context, events []storage.MLFeedbackEvent) error {
	apiFeedback[s] = append(apiFeedback[s], events...)
	return nil
}
func (s *modelAPIStore) CountMLFeedback(_ context.Context, vhost, model string, since time.Time) (map[string]int, error) {
	out := map[string]int{}
	for _, e := range apiFeedback[s] {
		if e.VHost == vhost && e.Model == model && !e.At.Before(since) {
			out[e.Outcome]++
		}
	}
	return out, nil
}
func (s *modelAPIStore) DeleteMLFeedback(context.Context, string, string) error { return nil }
func (s *modelAPIStore) ListMLSchedules(context.Context) ([]storage.MLSchedule, error) {
	var out []storage.MLSchedule
	for _, sc := range apiSchedules[s] {
		out = append(out, sc)
	}
	return out, nil
}
func (s *modelAPIStore) GetMLSchedule(_ context.Context, vhost, kind, name string) (storage.MLSchedule, error) {
	sc, ok := apiSchedules[s][vhost+"/"+kind+"/"+name]
	if !ok {
		return sc, storage.ErrNotFound
	}
	return sc, nil
}
func (s *modelAPIStore) PutMLSchedule(_ context.Context, sc storage.MLSchedule) error {
	if apiSchedules[s] == nil {
		apiSchedules[s] = map[string]storage.MLSchedule{}
	}
	sc.UpdatedAt = time.Now().UTC()
	apiSchedules[s][sc.VHost+"/"+sc.Kind+"/"+sc.Name] = sc
	return nil
}
func (s *modelAPIStore) DeleteMLSchedule(_ context.Context, vhost, kind, name string) error {
	k := vhost + "/" + kind + "/" + name
	if _, ok := apiSchedules[s][k]; !ok {
		return storage.ErrNotFound
	}
	delete(apiSchedules[s], k)
	return nil
}
func (s *modelAPIStore) SetMLScheduleRun(context.Context, string, string, string, time.Time, int, string) error {
	return nil
}
func (s *modelAPIStore) ClaimMLSchedule(context.Context, string, string, string, string, time.Duration) (bool, error) {
	return true, nil
}
func (s *modelAPIStore) ReleaseMLSchedule(context.Context, string, string, string, string) error {
	return nil
}
func (s *modelAPIStore) DeleteMLSchedules(context.Context, string) error { return nil }

// scenarioAPI is the model API over a worker holding the visits scenario
// fixture of pkg/ml/recommend as model "plan".
func scenarioAPI(t *testing.T) (*Handler, *modelAPIStore, *workerStub) {
	t.Helper()
	h, store, stub := newTrainingAPI(t)
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "pkg", "ml", "recommend", "testdata", "scenario", "visits.rec"))
	if err != nil {
		t.Fatal(err)
	}
	stub.rec = raw
	store.models["tenant-a/plan"] = storage.MLModel{VHost: "tenant-a", Name: "plan", Backend: storage.MLBackendWorker,
		RemoteVersion: "1", Task: storage.MLTaskRecommendation}
	return h, store, stub
}

func TestRecommendAnswersAScenario(t *testing.T) {
	h, _, _ := scenarioAPI(t)
	w := call(h.RecommendScenario, editorA, http.MethodPost, "tenant-a", "plan",
		`{"for":{"salesman":"R03"},"period":{"from":"2026-10-05","to":"2026-10-09"},"k":4}`)
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var out struct {
		RecommendationID string `json:"recommendation_id"`
		Results          []struct {
			For   string           `json:"for"`
			Items []map[string]any `json:"items"`
			Plan  []struct {
				Date  string           `json:"date"`
				Items []map[string]any `json:"items"`
			} `json:"plan"`
		} `json:"results"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.RecommendationID, "rec_") || len(out.Results) != 1 || out.Results[0].For != "R03" ||
		len(out.Results[0].Items) != 4 || out.Results[0].Plan[0].Date != "2026-10-05" {
		t.Fatalf("%s", w.Body)
	}
	item := out.Results[0].Items[0]
	for _, key := range []string{"id", "score", "rank", "reasons", "fields"} {
		if _, ok := item[key]; !ok {
			t.Errorf("an item has no %q: %v", key, item)
		}
	}

	for body, want := range map[string]string{
		`{"for":{"salesman":"R03"},"callback_url":"https://example.com"}`: "async",
		`{"for":{"salesman":"R03"},"respond":"later"}`:                    "respond",
		`{"for":{"customer":"C0001"}}`:                                    "salesman",
		`{"for":{"salesman":"R03"},"rules":[{"kind":"pin"}]}`:             "ids",
		`not json`: "JSON",
	} {
		w := call(h.RecommendScenario, editorA, http.MethodPost, "tenant-a", "plan", body)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), want) {
			t.Errorf("%s: %d %s, want 400 mentioning %q", body, w.Code, w.Body, want)
		}
	}
	if w := call(h.RecommendScenario, viewerA, http.MethodPost, "tenant-a", "plan", `{"for":{"salesman":"R03"}}`); w.Code != http.StatusForbidden {
		t.Errorf("a viewer: %d", w.Code)
	}
	if w := call(h.ServeRecommend, nil, http.MethodPost, "tenant-a", "plan", `{"for":{"salesman":"R03"}}`); w.Code != http.StatusUnauthorized {
		t.Errorf("no key: %d", w.Code)
	}
}

func TestLegacyPredictCarriesARecommendationID(t *testing.T) {
	h, _ := recommenderAPI(t)
	w := call(h.Predict, editorA, http.MethodPost, "tenant-a", "recs", `{"user":"1000","k":2}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"recommendation_id":"rec_`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestRecommendCheckRefusesAnEmptyBodyAndPassesAScenarioOn(t *testing.T) {
	h, _, stub := scenarioAPI(t)
	// The UI probes with an empty recommendation body: anything but
	// 404/405/501 says the route exists.
	w := call(h.RecommendCheck, editorA, http.MethodPost, "tenant-a", "", `{"task":"recommendation"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("probe: %d %s", w.Code, w.Body)
	}
	body := `{"task":"recommendation","scenario":"visits","dataset":"orders","target":"",` +
		`"data":{"transactions":{"dataset":"orders","customer":"cust_id","date":"order_date"},"customers":{"dataset":"customers","id":"cust_id","salesman":"rep"}},` +
		`"rules":[{"kind":"capacity","per_day":4}]}`
	w = call(h.RecommendCheck, editorA, http.MethodPost, "tenant-a", "", body)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"few_customers"`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if strings.Contains(stub.checked, `"rules"`) || !strings.Contains(stub.checked, `"scenario":"visits"`) {
		t.Errorf("sent the worker %s", stub.checked)
	}
}

func TestTrainAcceptsAScenarioBody(t *testing.T) {
	h, _, stub := scenarioAPI(t)
	body := `{"task":"recommendation","scenario":"visits","dataset":"orders","target":"",` +
		`"data":{"transactions":{"dataset":"orders","customer":"cust_id","date":"order_date"}},"go_live":{"mode":"always"}}`
	w := call(h.TrainModel, editorA, http.MethodPost, "tenant-a", "plan2", body)
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if !strings.Contains(stub.trained, `"scenario":"visits"`) || !strings.Contains(stub.trained, `"dataset":"orders"`) {
		t.Errorf("sent the worker %s", stub.trained)
	}
	// Without dataset, the transactions dataset stands in.
	body = strings.Replace(body, `"dataset":"orders","target":"",`, "", 1)
	if w := call(h.TrainModel, editorA, http.MethodPost, "tenant-a", "plan3", body); w.Code != http.StatusOK {
		t.Fatalf("without dataset: %d %s", w.Code, w.Body)
	}
}

func TestFeedbackRoundTrip(t *testing.T) {
	h, _, _ := scenarioAPI(t)
	w := call(h.PostFeedback, editorA, http.MethodPost, "tenant-a", "plan",
		`{"recommendation_id":"rec_x","events":[{"id":"C1","outcome":"shown"},{"id":"C2","outcome":"shown"},{"id":"C1","outcome":"accepted"}]}`)
	if w.Code != http.StatusNoContent {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := call(h.PostFeedback, editorA, http.MethodPost, "tenant-a", "plan", `{"recommendation_id":"rec_x","events":[{"id":"C1","outcome":"loved"}]}`); w.Code != http.StatusBadRequest {
		t.Fatalf("bad outcome: %d %s", w.Code, w.Body)
	}
	w = do(h.GetFeedbackSummary, viewerA, http.MethodGet, "/x?days=7", nil, map[string]string{"vhost": "tenant-a", "name": "plan"})
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var sum map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &sum)
	if sum["shown"] != 2.0 || sum["accepted"] != 1.0 || sum["acceptance_rate"] != 0.5 {
		t.Fatalf("summary %s", w.Body)
	}
}

func TestDatasetRefreshAndModelBuildSchedules(t *testing.T) {
	h, _, _ := scenarioAPI(t)
	values := map[string]string{"vhost": "tenant-a", "name": "customers"}
	w := do(h.PutDatasetRefresh, editorA, http.MethodPut, "/x", strings.NewReader(`{"schedule":"@daily","source_id":"pg","query":"SELECT * FROM c"}`), values)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"next_run"`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	w = do(h.GetDataset, editorA, http.MethodGet, "/x", nil, values)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"refresh":{"schedule":"@daily"`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := do(h.PutDatasetRefresh, editorA, http.MethodPut, "/x", strings.NewReader(`{"schedule":"sometimes","source_id":"pg","query":"q"}`), values); w.Code != http.StatusBadRequest {
		t.Fatalf("bad cron: %d %s", w.Code, w.Body)
	}
	if w := do(h.DeleteDatasetRefresh, editorA, http.MethodDelete, "/x", nil, values); w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	if w := do(h.DeleteDatasetRefresh, editorA, http.MethodDelete, "/x", nil, values); w.Code != http.StatusNotFound {
		t.Fatalf("second delete: %d %s", w.Code, w.Body)
	}
	if w := do(h.GetDataset, editorA, http.MethodGet, "/x", nil, values); !strings.Contains(w.Body.String(), `"refresh":null`) {
		t.Fatalf("after delete: %s", w.Body)
	}

	model := map[string]string{"vhost": "tenant-a", "name": "plan"}
	build := `{"schedule":"0 5 * * 1","for":"all_salesmen","period_days":5,"k":20,"target":{"source_id":"pg","table":"visit_plan","mode":"replace"}}`
	if w := do(h.PutModelBuild, editorA, http.MethodPut, "/x", strings.NewReader(build), model); w.Code != http.StatusOK {
		t.Fatalf("build: %d %s", w.Code, w.Body)
	}
	w = do(h.GetModelBuild, viewerA, http.MethodGet, "/x", nil, model)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"table":"visit_plan"`) {
		t.Fatalf("get build: %d %s", w.Code, w.Body)
	}
	if w := do(h.PutModelBuild, editorA, http.MethodPut, "/x", strings.NewReader(`{"schedule":"@daily","for":"everyone","target":{"source_id":"pg","table":"t"}}`), model); w.Code != http.StatusBadRequest {
		t.Fatalf("bad for: %d %s", w.Code, w.Body)
	}
	if w := do(h.DeleteModelBuild, editorA, http.MethodDelete, "/x", nil, model); w.Code != http.StatusNoContent {
		t.Fatalf("delete build: %d %s", w.Code, w.Body)
	}
}

// scenarioJobsAPI is newJobsAPI with the visits scenario fixture as model
// "plan" of tenant-a, on a worker stub.
func scenarioJobsAPI(t *testing.T) *Handler {
	t.Helper()
	h, st := newJobsAPI(t)
	h.noEnvWorker = false
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "pkg", "ml", "recommend", "testdata", "scenario", "visits.rec"))
	if err != nil {
		t.Fatal(err)
	}
	stub := &workerStub{rec: raw}
	h.worker = stub.start(t)
	if err := st.(storage.MLModelStore).PutMLModel(t.Context(), storage.MLModel{VHost: "tenant-a", Name: "plan",
		Backend: storage.MLBackendWorker, RemoteVersion: "1", Task: storage.MLTaskRecommendation}); err != nil {
		t.Fatal(err)
	}
	return h
}

// scenarioAnswer is a scenario call's answer, as far as these tests read it.
type scenarioAnswer struct {
	RecommendationID string `json:"recommendation_id"`
	Results          []struct {
		For   string            `json:"for"`
		Items []json.RawMessage `json:"items"`
		Plan  []struct {
			Date string `json:"date"`
		} `json:"plan"`
	} `json:"results"`
}

func TestAsyncScenarioRecommendAnswers202AndTheJobHoldsTheAnswer(t *testing.T) {
	h := scenarioJobsAPI(t)
	body := `{"for":{"salesman":"R03"},"period":{"from":"2026-10-05","to":"2026-10-09"},"k":4,"respond":"async"}`
	w := call(h.RecommendScenario, editorA, http.MethodPost, "tenant-a", "plan", body)
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
	var out scenarioAnswer
	if err := json.Unmarshal(j.Result, &out); err != nil || j.Status != storage.MLJobSucceeded || j.Kind != storage.MLJobRecommend {
		t.Fatalf("job = %+v result %s", j.MLJob, j.Result)
	}
	if !strings.HasPrefix(out.RecommendationID, "rec_") || len(out.Results) != 1 || out.Results[0].For != "R03" ||
		len(out.Results[0].Items) != 4 || out.Results[0].Plan[0].Date != "2026-10-05" {
		t.Errorf("result = %s", j.Result)
	}

	// The sync call's answer has the same shape.
	sync := call(h.RecommendScenario, editorA, http.MethodPost, "tenant-a", "plan", strings.Replace(body, `"respond":"async"`, `"respond":"sync"`, 1))
	var keys, jobKeys map[string]json.RawMessage
	if err := json.Unmarshal(sync.Body.Bytes(), &keys); err != nil || sync.Code != http.StatusOK {
		t.Fatalf("sync = %d %s", sync.Code, sync.Body)
	}
	_ = json.Unmarshal(j.Result, &jobKeys)
	if len(keys) != len(jobKeys) || keys["results"] == nil || jobKeys["results"] == nil {
		t.Errorf("sync %s, job %s", sync.Body, j.Result)
	}
}

func TestAsyncScenarioRecommendIsAskedForAsPredictIs(t *testing.T) {
	h := scenarioJobsAPI(t)
	acceptedOf(t, call(h.RecommendScenario, editorA, http.MethodPost, "tenant-a", "plan", `{"for":{"salesman":"R03"}}`,
		"Prefer", "respond-async"))
	// "respond": "sync" in the body wins over the header.
	if w := call(h.RecommendScenario, editorA, http.MethodPost, "tenant-a", "plan", `{"for":{"salesman":"R03"},"respond":"sync"}`,
		"Prefer", "respond-async"); w.Code != http.StatusOK {
		t.Errorf(`"respond": "sync" with Prefer: respond-async = %d %s`, w.Code, w.Body)
	}
	// What can be told before the job runs is refused at once.
	for body, want := range map[string]string{
		`{"for":{"customer":{}},"respond":"async"}`:                                  "id",
		`{"for":{"salesman":"R03"},"k":-1,"respond":"async"}`:                        "k",
		`{"for":{"salesman":"R03"},"period":{"from":"5/10/2026"},"respond":"async"}`: "period",
	} {
		if w := call(h.RecommendScenario, editorA, http.MethodPost, "tenant-a", "plan", body); w.Code != http.StatusBadRequest ||
			!strings.Contains(w.Body.String(), want) {
			t.Errorf("%s: %d %s, want 400 mentioning %q", body, w.Code, w.Body, want)
		}
	}
	if w := call(h.RecommendScenario, editorA, http.MethodPost, "tenant-a", "double", `{"for":{"salesman":"R03"},"respond":"async"}`); w.Code != http.StatusBadRequest {
		t.Errorf("an async scenario call of a model that predicts rows = %d %s", w.Code, w.Body)
	}
	// A callback needs the vhost's signing secret, as for predict.
	if w := call(h.RecommendScenario, editorA, http.MethodPost, "tenant-a", "plan",
		`{"for":{"salesman":"R03"},"respond":"async","callback_url":"https://hooks.example.com/done"}`); w.Code != http.StatusBadRequest ||
		!strings.Contains(w.Body.String(), ml.CallbackSecretName) {
		t.Errorf("a callback without the signing secret = %d %s", w.Code, w.Body)
	}
}

func TestAsyncScenarioRecommendWithAServingKey(t *testing.T) {
	h := scenarioJobsAPI(t)
	key := rotateKey(t, h, "plan")
	a := acceptedOf(t, call(h.ServeRecommend, nil, http.MethodPost, "tenant-a", "plan", `{"for":{"salesman":"R03"},"respond":"async"}`,
		"X-API-Key", key))
	if a.StatusURL != "/api/ml/serve/tenant-a/jobs/"+a.JobID {
		t.Fatalf("status_url = %q", a.StatusURL)
	}
	if w := call(h.ServeRecommend, nil, http.MethodPost, "tenant-a", "plan", `{"for":{"salesman":"R03"},"respond":"async"}`,
		"X-API-Key", "hml_wrong"); w.Code != http.StatusUnauthorized {
		t.Errorf("a wrong key queued a job: %d", w.Code)
	}
	runJobs(t, h)
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, a.StatusURL, nil)
	r.SetPathValue("vhost", "tenant-a")
	r.SetPathValue("id", a.JobID)
	r.Header.Set("X-API-Key", key)
	w := httptest.NewRecorder()
	h.ServeJob(w, r)
	j := jobOf(t, w)
	var out scenarioAnswer
	if err := json.Unmarshal(j.Result, &out); err != nil || j.Status != storage.MLJobSucceeded || len(out.Results) != 1 ||
		j.CallerKind != storage.MLCallerREST {
		t.Errorf("job = %+v result %s", j.MLJob, j.Result)
	}
}

func TestSyncScenarioRecommendOverTheTimeLimitIsToldToUseAsync(t *testing.T) {
	h := scenarioJobsAPI(t)
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(500 * time.Millisecond):
		case <-r.Context().Done():
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(slow.Close)
	h.worker = worker.New(slow.URL, "", nil)
	limit := 50 * time.Millisecond
	h.syncTimeout = &limit
	w := call(h.RecommendScenario, editorA, http.MethodPost, "tenant-a", "plan", `{"for":{"salesman":"R03"}}`)
	if w.Code != http.StatusRequestTimeout || !strings.Contains(w.Body.String(), "respond-async") {
		t.Errorf("a slow sync call = %d %s, want 408 pointing at async", w.Code, w.Body)
	}
}
