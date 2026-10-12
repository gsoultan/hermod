package ml

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/recommend"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// scenarioWorker is a worker holding version 1 of model "recs" in vhost "v":
// one of pkg/ml/recommend's scenario fixtures.
func scenarioWorker(t *testing.T, fixture string) *worker.Client {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(recFixtures, "scenario", fixture+".rec"))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/models/v/recs/versions/1/model.rec" {
			_, _ = rw.Write(raw)
			return
		}
		http.NotFound(rw, r)
	}))
	t.Cleanup(srv.Close)
	return worker.New(srv.URL, "", nil)
}

func scenarioService(t *testing.T, fixture string, quotas storage.MLQuotas) *Service {
	t.Helper()
	resetRecommenders()
	svc := NewService(func() any { return newMemStore(trainedRecs()) }, nil, nil).WithWorker(scenarioWorker(t, fixture)).
		WithQuotaDefaults(quotas)
	svc.now = func() time.Time { return time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC) }
	return svc
}

func TestRecommendScenarioAnswersWithAnID(t *testing.T) {
	svc := scenarioService(t, "visits", storage.MLQuotas{})
	out, err := svc.RecommendScenario(t.Context(), "v", "recs", ScenarioRequest{For: ScenarioFor{Salesman: "R03"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.RecommendationID, "rec_") || len(out.RecommendationID) < 20 {
		t.Fatalf("recommendation_id %q", out.RecommendationID)
	}
	if len(out.Results) != 1 || out.Results[0].For != "R03" || len(out.Results[0].Items) != 10 || len(out.Results[0].Plan) != 5 {
		t.Fatalf("results %+v", out.Results)
	}
	again, err := svc.RecommendScenario(t.Context(), "v", "recs", ScenarioRequest{For: ScenarioFor{Salesman: "R03"},
		Period: &ScenarioPeriod{From: "2026-10-05", To: "2026-10-05"}})
	if err != nil {
		t.Fatal(err)
	}
	if again.RecommendationID == out.RecommendationID || len(again.Results[0].Plan) != 1 {
		t.Fatalf("second call %+v", again)
	}
}

func TestRecommendScenarioRefusesWhatItCannotAnswer(t *testing.T) {
	cases := []struct {
		fixture string
		req     ScenarioRequest
		want    string
		is      error
	}{
		{"visits", ScenarioRequest{For: ScenarioFor{Salesman: "R01"}, Respond: "async"}, "not available yet", ErrBadRecommendRequest},
		{"visits", ScenarioRequest{For: ScenarioFor{Salesman: "R01"}, CallbackURL: "https://example.com/cb"}, "not available yet", ErrBadRecommendRequest},
		{"visits", ScenarioRequest{For: ScenarioFor{Salesman: "R01"}, Respond: "later"}, "respond", ErrBadRecommendRequest},
		{"visits", ScenarioRequest{For: ScenarioFor{Customer: "C0001"}}, "salesman", ErrBadRecommendRequest},
		{"visits", ScenarioRequest{For: ScenarioFor{Salesman: "R01"}, K: recommend.MaxPlan + 1}, "k", ErrBadRecommendRequest},
		{"products", ScenarioRequest{For: ScenarioFor{Customer: "C0001"}, K: recommend.MaxK + 1}, "k", ErrBadRecommendRequest},
		{"visits", ScenarioRequest{For: ScenarioFor{Salesman: "R01"}, Period: &ScenarioPeriod{From: "13/10/2026"}}, "period", ErrBadRecommendRequest},
		{"products", ScenarioRequest{For: ScenarioFor{Customer: "C0001"}, Period: &ScenarioPeriod{From: "2026-10-13"}}, "visit plan", ErrBadRecommendRequest},
		{"products", ScenarioRequest{For: ScenarioFor{Customer: map[string]any{}}}, "id", ErrBadRecommendRequest},
		{"products", ScenarioRequest{For: ScenarioFor{Customers: make([]any, recommend.MaxBatch+1)}}, "at most", ErrBadRecommendRequest},
		{"products", ScenarioRequest{For: ScenarioFor{Customer: "C0001"}, Rules: []recommend.Rule{{Kind: "teleport"}}}, "rule", ErrBadRecommendRequest},
		{"visits", ScenarioRequest{For: ScenarioFor{Salesman: "R01"}, Rules: []recommend.Rule{{Kind: recommend.RuleCapacity, PerDay: 2}}}, "", nil},
	}
	for _, tc := range cases {
		svc := scenarioService(t, tc.fixture, storage.MLQuotas{})
		_, err := svc.RecommendScenario(t.Context(), "v", "recs", tc.req)
		if tc.is == nil {
			if err != nil {
				t.Errorf("%+v: %v", tc.req, err)
			}
			continue
		}
		if !errors.Is(err, tc.is) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: %v, want %v mentioning %q", tc.req, err, tc.is, tc.want)
		}
	}
}

func TestRecommendScenarioCountsEverySubjectAgainstTheQuota(t *testing.T) {
	svc := scenarioService(t, "products", storage.MLQuotas{MaxPredictionsPerSecond: new(2.0)})
	_, err := svc.RecommendScenario(t.Context(), "v", "recs", ScenarioRequest{For: ScenarioFor{Customers: []any{"C0001", "C0002", "C0003", "C0004", "C0005"}}})
	var quota *QuotaError
	if !errors.As(err, &quota) {
		t.Fatalf("a batch of 5 at 2 a second: %v", err)
	}
}

func TestRecommendScenarioBindsTerritory(t *testing.T) {
	svc := scenarioService(t, "reorder", storage.MLQuotas{})
	rules := []recommend.Rule{{Kind: recommend.RuleFilter, Field: "customers.territory", Op: recommend.OpEq, FromRequest: recommend.FromTerritory}}
	for _, req := range []ScenarioRequest{
		{For: ScenarioFor{Salesman: "R02", Territory: "north"}, Rules: rules},
		{For: ScenarioFor{Salesman: "R02"}, Territory: "north", Rules: rules},
	} {
		out, err := svc.RecommendScenario(t.Context(), "v", "recs", req)
		if err != nil {
			t.Fatal(err)
		}
		if n := len(out.Results[0].Items); n != 0 {
			t.Errorf("%+v: %d of R02's customers (all south) listed in north", req, n)
		}
	}
}

func TestLegacyRecommendCarriesAnID(t *testing.T) {
	svc := scenarioService(t, "products", storage.MLQuotas{})
	items, id, err := svc.RecommendWithID(t.Context(), "v", "recs", RecommendRequest{User: "C0001"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 || !strings.HasPrefix(id, "rec_") {
		t.Fatalf("%d items, id %q", len(items), id)
	}
}

func TestScenarioTrainingSpecs(t *testing.T) {
	data := map[string]map[string]string{"transactions": {"dataset": "orders", "customer": "c", "product": "p"}}
	good := worker.TrainSpec{Task: worker.TaskRecommendation, Scenario: "visits", Data: data, Target: ""}
	spec, err := scenarioSpec(good)
	if err != nil || spec.Dataset != "orders" {
		t.Fatalf("scenarioSpec = %+v, %v", spec, err)
	}
	// The UI also sends the transactions dataset as dataset, and target "".
	withDataset := good
	withDataset.Dataset = "orders"
	if _, err := scenarioSpec(withDataset); err != nil {
		t.Fatalf("dataset = transactions: %v", err)
	}
	bad := []struct {
		spec worker.TrainSpec
		want string
	}{
		{worker.TrainSpec{Task: worker.TaskRecommendation, Scenario: "upsell", Data: data}, "scenario"},
		{worker.TrainSpec{Task: worker.TaskRecommendation, Scenario: "visits"}, "transactions"},
		{worker.TrainSpec{Task: worker.TaskRecommendation, Scenario: "visits", Data: data, Dataset: "other"}, "dataset"},
		{worker.TrainSpec{Task: worker.TaskRecommendation, Scenario: "visits", Data: data, Recommend: &worker.RecommendSpec{User: "u", Item: "i"}}, "recommend"},
		{worker.TrainSpec{Task: worker.TaskRecommendation, Scenario: "products", Data: data, Rules: []recommend.Rule{{Kind: recommend.RuleCapacity, PerDay: 2}}}, "rule"},
		{worker.TrainSpec{Task: worker.TaskRecommendation, Scenario: "visits", Data: data, PeriodDays: 400}, "period_days"},
		{worker.TrainSpec{Task: worker.TaskRecommendation, Scenario: "visits", Data: data, Device: DeviceGPU}, "CPU"},
	}
	for _, tc := range bad {
		if _, err := scenarioSpec(tc.spec); !errors.Is(err, ErrBadTraining) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: %v, want ErrBadTraining mentioning %q", tc.spec, err, tc.want)
		}
	}
	if err := checkRecommendSpec(good); err != nil {
		t.Errorf("checkRecommendSpec(scenario) = %v", err)
	}
}

func TestRecommendCheckNeedsAScenario(t *testing.T) {
	svc := NewService(func() any { return newMemStore() }, nil, nil).WithWorker(worker.New("http://127.0.0.1:1", "", nil))
	for _, spec := range []worker.TrainSpec{{Task: worker.TaskRecommendation}, {Task: "classification", Dataset: "d", Target: "t"}} {
		if _, err := svc.RecommendCheck(t.Context(), "v", spec); !errors.Is(err, ErrBadTraining) {
			t.Errorf("%+v: %v, want ErrBadTraining", spec, err)
		}
	}
}
