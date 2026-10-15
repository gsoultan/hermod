// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package ml

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
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
		{"visits", ScenarioRequest{For: ScenarioFor{Salesman: "R01"}, Respond: "async"}, "synchronously", ErrBadRecommendRequest},
		{"visits", ScenarioRequest{For: ScenarioFor{Salesman: "R01"}, CallbackURL: "https://example.com/cb"}, "synchronously", ErrBadRecommendRequest},
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

func TestRecommendScenarioWithinDays(t *testing.T) {
	svc := scenarioService(t, "reorder", storage.MLQuotas{})
	// Today is 2026-10-01: a 14-day window ends before 2026-10-15.
	for _, f := range []ScenarioFor{{Salesman: "R01"}, {AllCustomers: true}, {Product: "S007"}} {
		out, err := svc.RecommendScenario(t.Context(), "v", "recs", ScenarioRequest{For: f, WithinDays: 14, K: recommend.MaxPlan})
		if err != nil {
			t.Fatalf("%+v: %v", f, err)
		}
		items := out.Results[0].Items
		if len(items) == 0 {
			t.Fatalf("%+v: nobody due in 14 days", f)
		}
		for _, p := range items {
			if p.DueDate >= "2026-10-15" || p.Overdue != (p.DueDate < "2026-10-01") {
				t.Errorf("%+v: %s due %s, overdue %v", f, p.ID, p.DueDate, p.Overdue)
			}
		}
	}
	cases := []struct {
		fixture string
		req     ScenarioRequest
		want    string
	}{
		{"reorder", ScenarioRequest{For: ScenarioFor{Salesman: "R01"}, WithinDays: -1}, "within_days"},
		{"reorder", ScenarioRequest{For: ScenarioFor{Salesman: "R01"}, WithinDays: recommend.MaxWithinDays + 1}, "within_days"},
		{"visits", ScenarioRequest{For: ScenarioFor{Salesman: "R01"}, WithinDays: 14}, "reorder"},
		{"reorder", ScenarioRequest{For: ScenarioFor{AllCustomers: true}, K: recommend.MaxPlan + 1}, "k"},
		{"reorder", ScenarioRequest{For: ScenarioFor{AllCustomers: true, Salesman: "R01"}}, "one of"},
	}
	for _, tc := range cases {
		svc := scenarioService(t, tc.fixture, storage.MLQuotas{})
		_, err := svc.RecommendScenario(t.Context(), "v", "recs", tc.req)
		if !errors.Is(err, ErrBadRecommendRequest) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s %+v: %v, want ErrBadRecommendRequest mentioning %q", tc.fixture, tc.req, err, tc.want)
		}
	}
	// A window is checked before an async job is queued.
	if err := (ScenarioRequest{For: ScenarioFor{Salesman: "R01"}, WithinDays: 400}).check(); !errors.Is(err, ErrBadRecommendRequest) {
		t.Errorf("check: %v", err)
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
		{worker.TrainSpec{Task: worker.TaskRecommendation, Scenario: "products", Data: data, NearKm: 600}, "near_km"},
		{worker.TrainSpec{Task: worker.TaskRecommendation, Scenario: "products", Data: data, NearKm: 0.01}, "near_km"},
		{worker.TrainSpec{Task: worker.TaskRecommendation, Scenario: "products", Data: data, NearPeers: 2}, "near_peers"},
		{worker.TrainSpec{Task: worker.TaskRecommendation, Scenario: "products", Data: data, NewDays: 400}, "new_days"},
		{worker.TrainSpec{Task: worker.TaskRecommendation, Scenario: "products", Data: data, NewDays: -1}, "new_days"},
		{worker.TrainSpec{Task: worker.TaskRecommendation, Scenario: "products", Data: data, NearPeers: 500}, "near_peers"},
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
	// Nearby peers' radius and count reach the worker as asked.
	near := good
	near.NearKm, near.NearPeers = 2.5, 8
	if _, err := scenarioSpec(near); err != nil {
		t.Fatalf("near: %v", err)
	}
	body, _ := json.Marshal(near)
	if !strings.Contains(string(body), `"near_km":2.5`) || !strings.Contains(string(body), `"near_peers":8`) {
		t.Errorf("body %s", body)
	}
	// So do the days a product counts as new.
	fresh := good
	fresh.NewDays = 30
	if _, err := scenarioSpec(fresh); err != nil {
		t.Fatalf("new_days: %v", err)
	}
	if body, _ := json.Marshal(fresh); !strings.Contains(string(body), `"new_days":30`) {
		t.Errorf("body %s", body)
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

func TestRecommendScenarioKeepsACategoryAndSize(t *testing.T) {
	svc := scenarioService(t, "reorder_peers", storage.MLQuotas{})
	// "Customers in category A, large, likely to buy in the next 14 days."
	out, err := svc.RecommendScenario(t.Context(), "v", "recs", ScenarioRequest{
		For: ScenarioFor{AllCustomers: true, Segment: "A", Size: "large"}, WithinDays: 14, K: recommend.MaxPlan})
	if err != nil {
		t.Fatal(err)
	}
	items := out.Results[0].Items
	if len(items) == 0 {
		t.Fatal("no large A customers due in 14 days")
	}
	for _, p := range items {
		if p.Fields["segment"] != "A" || p.Fields["size"] != "large" {
			t.Fatalf("%s: fields %v", p.ID, p.Fields)
		}
	}
	// A size may be a number, cut as training cut the sizes; a category a number id.
	if _, err := svc.RecommendScenario(t.Context(), "v", "recs", ScenarioRequest{
		For: ScenarioFor{AllCustomers: true, Size: 1000.0}, K: recommend.MaxPlan}); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		fixture string
		req     ScenarioRequest
		want    string
	}{
		{"reorder_peers", ScenarioRequest{For: ScenarioFor{AllCustomers: true, Segment: map[string]any{"a": 1}}}, "segment"},
		{"reorder_peers", ScenarioRequest{For: ScenarioFor{AllCustomers: true, Size: true}}, "size"},
		{"products_peers", ScenarioRequest{For: ScenarioFor{Customer: "C0001", Segment: "A"}}, "customer lists"},
		{"reorder", ScenarioRequest{For: ScenarioFor{AllCustomers: true, Size: "large"}}, "size"},
	}
	for _, tc := range cases {
		svc := scenarioService(t, tc.fixture, storage.MLQuotas{})
		_, err := svc.RecommendScenario(t.Context(), "v", "recs", tc.req)
		if !errors.Is(err, ErrBadRecommendRequest) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s %+v: %v, want ErrBadRecommendRequest mentioning %q", tc.fixture, tc.req, err, tc.want)
		}
	}
	// A bad category is checked before an async job is queued.
	if err := (ScenarioRequest{For: ScenarioFor{AllCustomers: true, Segment: []any{}}}).check(); !errors.Is(err, ErrBadRecommendRequest) {
		t.Errorf("check: %v", err)
	}
}

func TestRecommendScenarioKeepsABrandAndSubcategoryAndTakesStock(t *testing.T) {
	svc := scenarioService(t, "products_shop", storage.MLQuotas{})
	// "Indomie products for this Sedaap shop."
	out, err := svc.RecommendScenario(t.Context(), "v", "recs", ScenarioRequest{For: ScenarioFor{Customer: "K30", Brand: "indomie"}, K: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Results[0].Items) == 0 {
		t.Fatal("no Indomie products")
	}
	for _, p := range out.Results[0].Items {
		if p.Fields["brand"] != "Indomie" || p.Fields["subcategory"] != "Instant noodles" {
			t.Fatalf("%s: fields %v", p.ID, p.Fields)
		}
	}
	// What it has bought comes with a quantity to suggest.
	all := false
	out, err = svc.RecommendScenario(t.Context(), "v", "recs", ScenarioRequest{For: ScenarioFor{Customer: "K02", Subcategory: "Instant noodles"},
		K: 20, ExcludeSeen: &all})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(out.Results[0].Items, func(p recommend.Pick) bool { return p.Fields["suggested_text"] == "3 cartons (100 pcs)" }) {
		t.Fatalf("no suggestion: %+v", out.Results[0].Items)
	}
	// Today's stock replaces the training's: I4 is back.
	out, err = svc.RecommendScenario(t.Context(), "v", "recs", ScenarioRequest{For: ScenarioFor{Customer: "K01"}, K: 20,
		Stock: map[string]float64{"I4": 12}})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(out.Results[0].Items, func(p recommend.Pick) bool { return p.ID == "I4" }) {
		t.Fatalf("restocked I4 not offered: %+v", out.Results[0].Items)
	}
	cases := []struct {
		fixture string
		req     ScenarioRequest
		want    string
	}{
		{"products_shop", ScenarioRequest{For: ScenarioFor{Customer: "K01", Brand: map[string]any{"a": 1}}}, "brand"},
		{"products_shop", ScenarioRequest{For: ScenarioFor{Customer: "K01", Subcategory: true}}, "subcategory"},
		{"audience_shop", ScenarioRequest{For: ScenarioFor{Product: "I1", Brand: "Indomie"}}, "product lists"},
		{"products_peers", ScenarioRequest{For: ScenarioFor{Customer: "C0001", Brand: "Acme"}}, "brand"},
	}
	for _, tc := range cases {
		svc := scenarioService(t, tc.fixture, storage.MLQuotas{})
		_, err := svc.RecommendScenario(t.Context(), "v", "recs", tc.req)
		if !errors.Is(err, ErrBadRecommendRequest) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s %+v: %v, want ErrBadRecommendRequest mentioning %q", tc.fixture, tc.req, err, tc.want)
		}
	}
	// Too much stock is refused before an async job is queued.
	stock := map[string]float64{}
	for i := range recommend.MaxStock + 1 {
		stock[strconv.Itoa(i)] = 1
	}
	if err := (ScenarioRequest{For: ScenarioFor{Customer: "K01"}, Stock: stock}).check(); !errors.Is(err, ErrBadRecommendRequest) {
		t.Errorf("check: %v", err)
	}
}

func TestRecommendScenarioKeepsAPriceTierChannelAndRegion(t *testing.T) {
	svc := scenarioService(t, "audience_market", storage.MLQuotas{})
	out, err := svc.RecommendScenario(t.Context(), "v", "recs", ScenarioRequest{
		For: ScenarioFor{Product: "S1", PriceTier: "Gold", Channel: "Retail", Region: "N"}, K: recommend.MaxK})
	if err != nil {
		t.Fatal(err)
	}
	items := out.Results[0].Items
	if len(items) == 0 {
		t.Fatal("no Gold Retail customers in N")
	}
	for _, p := range items {
		if p.Fields["price_tier"] != "Gold" || p.Fields["channel"] != "Retail" || p.Fields["region"] != "N" {
			t.Fatalf("%s: fields %v", p.ID, p.Fields)
		}
	}
	for _, tc := range []struct {
		req  ScenarioRequest
		want string
	}{
		{ScenarioRequest{For: ScenarioFor{Product: "S1", PriceTier: map[string]any{"a": 1}}}, "price_tier"},
		{ScenarioRequest{For: ScenarioFor{Product: "S1", Channel: true}}, "channel"},
		{ScenarioRequest{For: ScenarioFor{Product: "S1", Region: []any{}}}, "region"},
	} {
		if _, err := svc.RecommendScenario(t.Context(), "v", "recs", tc.req); !errors.Is(err, ErrBadRecommendRequest) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: %v, want ErrBadRecommendRequest mentioning %q", tc.req, err, tc.want)
		}
	}
}

func TestScenarioTrainingTakesReturnsAndQuotes(t *testing.T) {
	data := map[string]map[string]string{"transactions": {"dataset": "orders", "customer": "c", "product": "p"},
		"returns": {"dataset": "returns", "customer": "c", "product": "p", "date": "d"},
		"quotes":  {"dataset": "quotes", "customer": "c", "product": "p", "date": "d", "outcome": "o"}}
	if _, err := scenarioSpec(worker.TrainSpec{Task: worker.TaskRecommendation, Scenario: "products", Data: data}); err != nil {
		t.Fatalf("returns and quotes: %v", err)
	}
}
