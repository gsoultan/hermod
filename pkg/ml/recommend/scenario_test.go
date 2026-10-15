// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package recommend

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fixtureToday is the day after the scenario fixtures' as_of, a Thursday.
var fixtureToday = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

func scenarioBytes(t testing.TB, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "scenario", name+".rec"))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// withHeader is a fixture artifact with its header changed by edit; the
// sections are kept as they are.
func withHeader(t testing.TB, raw []byte, edit func(h map[string]any)) []byte {
	t.Helper()
	n := binary.LittleEndian.Uint32(raw[8:])
	var h map[string]any
	if err := json.Unmarshal(raw[12:12+n], &h); err != nil {
		t.Fatal(err)
	}
	edit(h)
	head, err := json.Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	out := slices.Clone(raw[:8])
	out = binary.LittleEndian.AppendUint32(out, uint32(len(head)))
	out = append(out, head...)
	return append(out, raw[12+n:]...)
}

func scenarioModel(t testing.TB, name string, rules ...Rule) *Model {
	t.Helper()
	raw := scenarioBytes(t, name)
	if len(rules) > 0 {
		raw = withHeader(t, raw, func(h map[string]any) { h["rules"] = rules })
	}
	m, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse(%s): %v", name, err)
	}
	return m
}

func answer(t *testing.T, m *Model, q Query) Result {
	t.Helper()
	if q.Today.IsZero() {
		q.Today = fixtureToday
	}
	res, err := m.Answer(q)
	if err != nil {
		t.Fatalf("Answer(%+v): %v", q, err)
	}
	if len(res) != 1 {
		t.Fatalf("%d results", len(res))
	}
	return res[0]
}

func ids(picks []Pick) []string {
	out := make([]string, len(picks))
	for i, p := range picks {
		out[i] = p.ID
	}
	return out
}

func TestParseReadsAScenarioArtifact(t *testing.T) {
	m := scenarioModel(t, "visits")
	if m.Scenario() != ScenarioVisits || m.PeriodDays() != 7 || !m.AsOf().Equal(time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("scenario %q, period %d, as of %v", m.Scenario(), m.PeriodDays(), m.AsOf())
	}
	if got := m.Salesmen(); !slices.Equal(got, []string{"R01", "R02", "R03", "R04"}) {
		t.Fatalf("salesmen %v", got)
	}
	// A reorder model keeps its products for their due dates, with no
	// recommender; one trained without products has none.
	r := scenarioModel(t, "reorder")
	if r.Algorithm() != AlgorithmNone || r.Items() == 0 {
		t.Fatalf("reorder: algorithm %q with %d items", r.Algorithm(), r.Items())
	}
	if c := scenarioModel(t, "reorder_customers"); c.Algorithm() != AlgorithmNone || c.Items() != 0 {
		t.Fatalf("reorder without products: algorithm %q with %d items", c.Algorithm(), c.Items())
	}
	// A format 1 model answers the products scenario.
	if v1 := mustParse(t, factorsArtifact()); v1.Scenario() != ScenarioProducts || v1.DefaultRules() != nil {
		t.Fatalf("format 1: scenario %q", v1.Scenario())
	}
}

func TestParseRefusesAMalformedScenarioArtifact(t *testing.T) {
	cases := []struct {
		name string
		edit func(h map[string]any)
		want string
	}{
		{"unknown scenario", func(h map[string]any) { h["scenario"] = "upsell" }, "scenario"},
		{"bad as_of", func(h map[string]any) { h["as_of"] = "yesterday" }, "as_of"},
		{"negative period", func(h map[string]any) { h["period_days"] = -1 }, "period_days"},
		{"too many customer neighbours", func(h map[string]any) { h["customer_neighbors"] = MaxCustomerNeighbors + 1 }, "customer_neighbors"},
		{"short attribute", func(h map[string]any) { h["customer_attrs"].(map[string]any)["segment"] = []string{"A"} }, "segment"},
		{"duplicate feature", func(h map[string]any) { h["item_features"] = []string{"price", "price", "a", "b", "c"} }, "twice"},
		{"unknown weight", func(h map[string]any) { h["weights"] = map[string]float64{"luck": 0.5} }, "luck"},
		{"bad working day", func(h map[string]any) {
			h["salesmen"] = map[string]any{"R01": map[string]any{"visits_per_day": 4, "working_days": []string{"someday"}}}
		}, "weekday"},
		{"bad capacity", func(h map[string]any) {
			h["salesmen"] = map[string]any{"R01": map[string]any{"visits_per_day": 0}}
		}, "visits per day"},
		{"bad rule", func(h map[string]any) { h["rules"] = []map[string]any{{"kind": "teleport"}} }, "rule 1"},
		{"none with neighbours", func(h map[string]any) { h["algorithm"] = AlgorithmNone }, "neither"},
		{"negative gap", func(h map[string]any) { h["global_gap"] = -3 }, "gap"},
		{"more features than the bytes", func(h map[string]any) {
			h["item_features"] = []string{"price", "margin", "margin_pct", "buyers", "popularity", "extra"}
		}, "bytes"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(withHeader(t, scenarioBytes(t, "visits"), tc.edit))
			if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Parse = %v, want ErrInvalid mentioning %q", err, tc.want)
			}
		})
	}
	t.Run("infinite feature", func(t *testing.T) {
		raw := slices.Clone(scenarioBytes(t, "visits"))
		binary.LittleEndian.PutUint32(raw[len(raw)-4:], math.Float32bits(float32(math.Inf(1))))
		if _, err := Parse(raw); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "infinite") {
			t.Fatalf("Parse = %v", err)
		}
	})
	t.Run("format 1 with none", func(t *testing.T) {
		b := factorsArtifact()
		b.header["algorithm"] = AlgorithmNone
		if _, err := Parse(b.encode(t)); !errors.Is(err, ErrInvalid) {
			t.Fatalf("Parse = %v", err)
		}
	})
}

func TestAnswerRefusesTheWrongSubject(t *testing.T) {
	cases := []struct {
		model string
		q     Query
		want  string
	}{
		{"visits", Query{Customer: "C0001"}, "salesman"},
		{"visits", Query{}, "one of"},
		{"visits", Query{Salesman: "R01", Customer: "C0001"}, "one of"},
		{"audience", Query{Customer: "C0001"}, "product"},
		{"products", Query{Salesman: "R01"}, "customer"},
		{"products", Query{Customers: make([]string, MaxBatch+1)}, "at most"},
		{"visits", Query{Salesman: "R01", From: fixtureToday, To: fixtureToday.AddDate(0, 0, -1)}, "period"},
	}
	for _, tc := range cases {
		tc.q.Today = fixtureToday
		_, err := scenarioModel(t, tc.model).Answer(tc.q)
		if !errors.Is(err, ErrBadQuery) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s %+v: %v, want ErrBadQuery mentioning %q", tc.model, tc.q, err, tc.want)
		}
	}
}

func TestAVisitPlanFitsTheSalesmansWeek(t *testing.T) {
	m := scenarioModel(t, "visits")
	res := answer(t, m, Query{Salesman: "R03"})
	// R03: 2 visits a day, Monday to Friday; Thursday 1 October for 7 days
	// has five working days.
	if len(res.Items) != 10 {
		t.Fatalf("%d planned, want 10", len(res.Items))
	}
	var dates []string
	planned := 0
	for _, d := range res.Plan {
		dates = append(dates, d.Date)
		if len(d.Items) > 2 {
			t.Errorf("%s: %d visits, over 2", d.Date, len(d.Items))
		}
		planned += len(d.Items)
	}
	if want := []string{"2026-10-01", "2026-10-02", "2026-10-05", "2026-10-06", "2026-10-07"}; !slices.Equal(dates, want) {
		t.Fatalf("dates %v, want %v", dates, want)
	}
	if planned != len(res.Items) {
		t.Fatalf("%d on the plan of %d", planned, len(res.Items))
	}
	// The most urgent customer opens the first day.
	if res.Plan[0].Items[0].ID != res.Items[0].ID {
		t.Fatalf("day 1 opens with %s, not %s", res.Plan[0].Items[0].ID, res.Items[0].ID)
	}
	for _, p := range res.Items {
		if p.Fields["salesman"] != "R03" || len(p.Reasons) == 0 {
			t.Fatalf("%+v", p)
		}
	}

	// A period and k narrow it.
	res = answer(t, m, Query{Salesman: "R03", From: time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)})
	if len(res.Plan) != 1 || res.Plan[0].Date != "2026-10-05" || len(res.Items) != 2 {
		t.Fatalf("one Monday: %+v", res.Plan)
	}
	res = answer(t, m, Query{Salesman: "R03", K: 3})
	if len(res.Items) != 3 {
		t.Fatalf("k=3: %d", len(res.Items))
	}
	// A weekend has no working day.
	res = answer(t, m, Query{Salesman: "R03", From: time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)})
	if len(res.Items) != 0 || len(res.Plan) != 0 {
		t.Fatalf("weekend: %+v", res)
	}
	// An unknown salesman has no customers.
	if res := answer(t, m, Query{Salesman: "nobody"}); len(res.Items) != 0 {
		t.Fatalf("nobody: %+v", res)
	}
}

func TestCapacityRules(t *testing.T) {
	m := scenarioModel(t, "visits", Rule{Kind: RuleCapacity, PerDay: 3, Value: []any{"mon", "tue", "wed"}})
	res := answer(t, m, Query{Salesman: "R01"})
	if len(res.Plan) != 3 || len(res.Items) != 9 {
		t.Fatalf("%d days, %d items; want 3 days of 3", len(res.Plan), len(res.Items))
	}
	// A request may tighten the capacity...
	res = answer(t, m, Query{Salesman: "R01", Rules: []Rule{{Kind: RuleCapacity, PerDay: 1, Value: []any{"mon"}}}})
	if len(res.Plan) != 1 || len(res.Items) != 1 {
		t.Fatalf("tightened: %+v", res.Plan)
	}
	// ...but not loosen it.
	for _, r := range []Rule{{Kind: RuleCapacity, PerDay: 5}, {Kind: RuleCapacity, Value: []any{"sat"}}} {
		if _, err := m.Answer(Query{Today: fixtureToday, Salesman: "R01", Rules: []Rule{r}}); !errors.Is(err, ErrLoosens) {
			t.Errorf("%+v: %v, want ErrLoosens", r, err)
		}
	}
}

func TestFilterExcludeBoostDiversityPinBlock(t *testing.T) {
	base := answer(t, scenarioModel(t, "visits"), Query{Salesman: "R01"})
	if len(base.Items) < 5 {
		t.Fatalf("%d items", len(base.Items))
	}

	// Filter: only segment A.
	m := scenarioModel(t, "visits", Rule{Kind: RuleFilter, Field: "customers.segment", Op: OpEq, Value: "A"})
	for _, p := range answer(t, m, Query{Salesman: "R01"}).Items {
		if p.Fields["segment"] != "A" {
			t.Fatalf("%s is segment %v", p.ID, p.Fields["segment"])
		}
	}

	// Block the best; pin the last; boost the fourth to the top of the rest.
	last := base.Items[len(base.Items)-1].ID
	m = scenarioModel(t, "visits", Rule{Kind: RuleBlock, IDs: []string{base.Items[0].ID}},
		Rule{Kind: RulePin, IDs: []string{last}}, Rule{Kind: RuleBoost, IDs: []string{base.Items[3].ID}, Weight: 1})
	got := answer(t, m, Query{Salesman: "R01"}).Items
	if slices.Contains(ids(got), base.Items[0].ID) {
		t.Fatalf("blocked %s listed", base.Items[0].ID)
	}
	if got[0].ID != last || got[0].Reasons[0].Code != ReasonPinned {
		t.Fatalf("first is %+v, want pinned %s", got[0], last)
	}
	if got[1].ID != base.Items[3].ID || got[1].Reasons[len(got[1].Reasons)-1].Code != ReasonBoosted {
		t.Fatalf("second is %+v, want boosted %s", got[1], base.Items[3].ID)
	}

	// Diversity: at most two per segment.
	m = scenarioModel(t, "visits", Rule{Kind: RuleDiversity, Field: "customers.segment", MaxPer: 2})
	counts := map[any]int{}
	for _, p := range answer(t, m, Query{Salesman: "R01"}).Items {
		counts[p.Fields["segment"]]++
	}
	for seg, n := range counts {
		if n > 2 {
			t.Fatalf("segment %v listed %d times", seg, n)
		}
	}

	// Exclude: products bought in the last 30 days are not offered again.
	p := scenarioModel(t, "products")
	all := answer(t, p, Query{Customer: "C0001", K: 50})
	ex := answer(t, p, Query{Customer: "C0001", K: 50, Rules: []Rule{{Kind: RuleExclude, Days: 30}}})
	if len(ex.Items) > len(all.Items) {
		t.Fatalf("exclude grew the list")
	}
	for _, it := range ex.Items {
		if c := (&call{m: p, today: dayOf(fixtureToday)}); c.boughtWithin(p.userIndex["C0001"], p.itemIndex[it.ID], 30) {
			t.Fatalf("%s was bought in the last 30 days", it.ID)
		}
	}

	// A request may not pin what the model's rules block or filter out.
	m = scenarioModel(t, "visits", Rule{Kind: RuleBlock, IDs: []string{"C0001"}}, Rule{Kind: RuleFilter, Field: "customers.segment", Op: OpEq, Value: "B"})
	for _, id := range []string{"C0001", "C0005"} { // C0005 is segment A.
		_, err := m.Answer(Query{Today: fixtureToday, Salesman: "R01", Rules: []Rule{{Kind: RulePin, IDs: []string{id}}}})
		if !errors.Is(err, ErrLoosens) {
			t.Errorf("pin %s: %v, want ErrLoosens", id, err)
		}
	}
}

func TestFromRequestBindsTheSalesmanAndTerritory(t *testing.T) {
	// A products model whose rule keeps the customer's own territory: the
	// customer is in it when the request's territory says so.
	m := scenarioModel(t, "reorder", Rule{Kind: RuleFilter, Field: "customers.territory", Op: OpEq, FromRequest: FromTerritory})
	got := answer(t, m, Query{Salesman: "R02", K: 50}).Items
	if len(got) == 0 {
		t.Fatal("no customers")
	}
	for _, p := range got {
		if p.Fields["territory"] != "south" {
			t.Fatalf("%s in %v; R02's customers are in south", p.ID, p.Fields["territory"])
		}
	}
	// A territory given outright wins over the salesman's.
	if got := answer(t, m, Query{Salesman: "R02", Territory: "north"}).Items; len(got) != 0 {
		t.Fatalf("R02's customers in north: %v", ids(got))
	}
	// Unbound (no salesman, no territory), the rule is skipped.
	if got := answer(t, m, Query{Customer: "C0001"}).Items; len(got) != 1 {
		t.Fatalf("C0001: %v", ids(got))
	}
}

func TestReorderAndAudienceAndBatches(t *testing.T) {
	r := scenarioModel(t, "reorder")
	res, err := r.Answer(Query{Today: fixtureToday, Customers: []string{"C0001", "C0002", "nobody"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 3 || res[0].For != "C0001" || len(res[0].Items) != 1 || res[0].Items[0].DueDate == "" || len(res[2].Items) != 0 {
		t.Fatalf("batch: %+v", res)
	}
	a := answer(t, scenarioModel(t, "audience"), Query{Product: "S001", K: 5, ExcludeSeen: true})
	if len(a.Items) != 5 {
		t.Fatalf("audience: %d", len(a.Items))
	}
	if a := answer(t, scenarioModel(t, "audience"), Query{Product: "nothing"}); len(a.Items) != 0 {
		t.Fatalf("unknown product: %+v", a)
	}
	// A customer the model does not know gets the popular products.
	p := answer(t, scenarioModel(t, "products"), Query{Customer: "stranger", K: 3})
	if len(p.Items) != 3 || p.Items[0].Reasons[0].Code != ReasonPopular {
		t.Fatalf("stranger: %+v", p.Items)
	}
}

func TestScheduleGroupsNearbyCustomers(t *testing.T) {
	at := func(id string, lat, lon float64) Pick {
		return Pick{ID: id, Fields: map[string]any{"lat": lat, "lon": lon}}
	}
	// Ranked a, b, c, d: a and c are in Berlin, b and d in Munich.
	items := []Pick{at("a", 52.5, 13.4), at("b", 48.1, 11.6), at("c", 52.51, 13.41), at("d", 48.11, 11.61), {ID: "e"}}
	plan := schedule(items, []int{0, 1, 2}, 2)
	var got [][]string
	for _, d := range plan {
		got = append(got, ids(d.Items))
	}
	want := [][]string{{"a", "c"}, {"b", "d"}, {"e"}}
	if len(got) != len(want) {
		t.Fatalf("plan %v, want %v", got, want)
	}
	for i := range want {
		if !slices.Equal(got[i], want[i]) {
			t.Fatalf("plan %v, want %v", got, want)
		}
	}
	if d := haversine(52.52, 13.405, 48.137, 11.575); math.Abs(d-504) > 2 {
		t.Fatalf("Berlin to Munich %v km", d)
	}
}

func TestValidateRules(t *testing.T) {
	bad := []struct {
		scenario string
		rule     Rule
		want     string
	}{
		{ScenarioVisits, Rule{Kind: "teleport"}, "kind"},
		{ScenarioVisits, Rule{Kind: RuleFilter, Field: "customers.segment", Op: "like", Value: "A"}, "op"},
		{ScenarioVisits, Rule{Kind: RuleFilter, Field: "segment", Op: OpEq, Value: "A"}, "customers.<name>"},
		{ScenarioVisits, Rule{Kind: RuleFilter, Field: "products.category", Op: OpEq, Value: "A"}, "does not apply"},
		{ScenarioVisits, Rule{Kind: RuleFilter, Field: "customers.segment", Op: OpIn, Value: "A"}, "list"},
		{ScenarioVisits, Rule{Kind: RuleFilter, Field: "customers.monetary", Op: OpGt, Value: "big"}, "number"},
		{ScenarioVisits, Rule{Kind: RuleFilter, Field: "customers.salesman", Op: OpEq, Value: "R01", FromRequest: FromSalesman}, "not both"},
		{ScenarioVisits, Rule{Kind: RuleFilter, Field: "customers.salesman", Op: OpEq, FromRequest: "boss"}, "from_request"},
		{ScenarioProducts, Rule{Kind: RuleExclude, Days: -1}, "days"},
		{ScenarioProducts, Rule{Kind: RuleBoost, Field: "products.margin"}, "weight"},
		{ScenarioProducts, Rule{Kind: RuleBoost, Field: "products.margin", Weight: 2}, "weight"},
		{ScenarioProducts, Rule{Kind: RuleBoost, Field: "products.margin", IDs: []string{"x"}, Weight: 0.5}, "not both"},
		{ScenarioProducts, Rule{Kind: RuleDiversity, Field: "products.category"}, "max_per"},
		{ScenarioProducts, Rule{Kind: RuleCapacity, PerDay: 3}, "visit plan"},
		{ScenarioVisits, Rule{Kind: RuleCapacity}, "per_day"},
		{ScenarioVisits, Rule{Kind: RuleCapacity, Value: []any{"someday"}}, "weekday"},
		{ScenarioVisits, Rule{Kind: RulePin}, "ids"},
		{ScenarioVisits, Rule{Kind: RuleBlock, IDs: []string{""}}, "ids"},
	}
	for _, tc := range bad {
		err := ValidateRules(tc.scenario, []Rule{tc.rule})
		if !errors.Is(err, ErrBadRule) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s %+v: %v, want ErrBadRule mentioning %q", tc.scenario, tc.rule, err, tc.want)
		}
	}
	if err := ValidateRules(ScenarioVisits, make([]Rule, MaxRules+1)); !errors.Is(err, ErrBadRule) {
		t.Errorf("too many rules: %v", err)
	}
	good := []struct {
		scenario string
		rule     Rule
	}{
		{ScenarioVisits, Rule{Kind: RuleFilter, Field: "customers.salesman", Op: OpEq, FromRequest: FromSalesman}},
		{ScenarioVisits, Rule{Kind: RuleCapacity, PerDay: 6, Value: []any{"mon", "TUE"}}},
		{ScenarioProducts, Rule{Kind: RuleFilter, Field: "customers.segment", Op: OpIn, Value: []any{"A", "B"}}},
		{ScenarioProducts, Rule{Kind: RuleFilter, Field: "products.active", Op: OpEq, Value: true}},
		{ScenarioProducts, Rule{Kind: RuleBoost, Field: "products.margin", Weight: 0.3}},
		{ScenarioProducts, Rule{Kind: RuleBoost, Field: "products.category", Op: OpEq, Value: "dairy", Weight: -0.2}},
		{ScenarioProducts, Rule{Kind: RuleExclude, Days: 30}},
		{ScenarioAudience, Rule{Kind: RuleDiversity, Field: "customers.territory", MaxPer: 3}},
	}
	for _, tc := range good {
		if err := ValidateRules(tc.scenario, []Rule{tc.rule}); err != nil {
			t.Errorf("%s %+v: %v", tc.scenario, tc.rule, err)
		}
	}
}

// windowEnd is the first day after a window of days from fixtureToday.
func windowEnd(days int) string {
	return fixtureToday.AddDate(0, 0, days).Format(time.DateOnly)
}

// checkWindow asserts what every answer of a window holds: each item due
// before the window's end, marked overdue exactly when due before today,
// ranked by likelihood and then by due date, with reasons.
func checkWindow(t *testing.T, items []Pick, days int) {
	t.Helper()
	today, end := fixtureToday.Format(time.DateOnly), windowEnd(days)
	for i, p := range items {
		if p.DueDate == "" || p.DueDate >= end {
			t.Errorf("%s: due %q, outside the %d-day window ending %s", p.ID, p.DueDate, days, end)
		}
		if p.Overdue != (p.DueDate < today) {
			t.Errorf("%s: due %s, overdue %v on %s", p.ID, p.DueDate, p.Overdue, today)
		}
		if p.Score < 0 || p.Score > 1 {
			t.Errorf("%s: likelihood %v outside [0, 1]", p.ID, p.Score)
		}
		if len(p.Reasons) == 0 {
			t.Errorf("%s: no reasons", p.ID)
		}
		if p.Rank != i+1 {
			t.Errorf("%s: rank %d at %d", p.ID, p.Rank, i)
		}
		if i > 0 {
			prev := items[i-1]
			if prev.Score < p.Score || (prev.Score == p.Score && prev.DueDate > p.DueDate) {
				t.Errorf("%s (%v, %s) ranked after %s (%v, %s)", p.ID, p.Score, p.DueDate, prev.ID, prev.Score, prev.DueDate)
			}
		}
	}
}

func TestReorderWithinDaysKeepsTheSalesmansCustomersDueInTheWindow(t *testing.T) {
	m := scenarioModel(t, "reorder")
	all := answer(t, m, Query{Salesman: "R01", K: MaxPlan})
	for _, days := range []int{14, 30} {
		got := answer(t, m, Query{Salesman: "R01", K: MaxPlan, WithinDays: days})
		checkWindow(t, got.Items, days)
		// The window is the unwindowed list's customers due before its end:
		// overdue customers stay in, marked.
		var want []string
		for _, p := range all.Items {
			if p.DueDate < windowEnd(days) {
				want = append(want, p.ID)
			}
		}
		gotIDs := ids(got.Items)
		slices.Sort(gotIDs)
		slices.Sort(want)
		if !slices.Equal(gotIDs, want) {
			t.Fatalf("%d days: customers %v, want %v", days, gotIDs, want)
		}
		overdue, upcoming := 0, 0
		for _, p := range got.Items {
			if p.Overdue {
				overdue++
			} else {
				upcoming++
			}
		}
		if overdue == 0 || upcoming == 0 {
			t.Fatalf("%d days: %d overdue and %d upcoming; the fixture has both", days, overdue, upcoming)
		}
	}
	// Each customer lists the products due in the window, soonest first,
	// leaving out those they stopped buying: three of their usual gaps and
	// more without one.
	got := answer(t, m, Query{Salesman: "R01", K: MaxPlan, WithinDays: 30})
	c := &call{m: m, today: dayOf(fixtureToday)}
	listed := 0
	for _, p := range got.Items {
		for j, d := range p.Products {
			listed++
			last, gap, ok := c.pairCadence(m.userIndex[p.ID], m.itemIndex[d.ID])
			if !ok || (float64(c.today)-last)/gap >= 3 {
				t.Errorf("%s: product %s is listed, %v gaps since it was last bought", p.ID, d.ID, (float64(c.today)-last)/gap)
			}
			if d.ID == "" || d.DueDate == "" || d.DueDate >= windowEnd(30) || d.Overdue != (d.DueDate < fixtureToday.Format(time.DateOnly)) {
				t.Errorf("%s: product %+v", p.ID, d)
			}
			if j > 0 && p.Products[j-1].DueDate > d.DueDate {
				t.Errorf("%s: products not soonest first: %+v", p.ID, p.Products)
			}
		}
	}
	if listed == 0 {
		t.Fatal("no customer lists a product due")
	}
}

func TestReorderWithoutAWindowAnswersAsBefore(t *testing.T) {
	m := scenarioModel(t, "reorder")
	got := answer(t, m, Query{Salesman: "R01", K: MaxPlan})
	for i, p := range got.Items {
		if len(p.Products) != 0 {
			t.Fatalf("%s lists products without a window", p.ID)
		}
		if p.Overdue != (p.DueDate < fixtureToday.Format(time.DateOnly)) {
			t.Errorf("%s: due %s, overdue %v", p.ID, p.DueDate, p.Overdue)
		}
		if i > 0 && got.Items[i-1].Score < p.Score {
			t.Fatalf("not most overdue first at %d", i)
		}
	}
}

func TestReorderForAProductListsItsBuyersDueAgain(t *testing.T) {
	m := scenarioModel(t, "reorder")
	product := m.items[0]
	i := m.itemIndex[product]
	// What the model knows: each customer's last day and usual gap for it.
	due := map[string]string{}
	for u := range m.users {
		seen := m.seenBy(u)
		k, found := slices.BinarySearch(seen, uint32(i))
		if !found {
			continue
		}
		pos := int(m.seenOff[u]) + k
		if g := float64(m.sc.seenGap[pos]); finite(g) && g > 0 {
			due[m.users[u]] = dayText(int(math.Ceil(float64(m.sc.seenLast[pos]) + g)))
		}
	}
	if len(due) < 2 {
		t.Fatalf("product %s has %d repeat buyers in the fixture", product, len(due))
	}
	all := answer(t, m, Query{Product: product, K: MaxPlan})
	if all.For != product || len(all.Items) != len(due) {
		t.Fatalf("for %q: %d customers, want %d", all.For, len(all.Items), len(due))
	}
	for _, p := range all.Items {
		if p.DueDate != due[p.ID] || len(p.Products) != 1 || p.Products[0].ID != product || p.Products[0].DueDate != p.DueDate {
			t.Errorf("%s: due %s with %+v, want %s", p.ID, p.DueDate, p.Products, due[p.ID])
		}
		if p.Reasons[0].Code != ReasonReorderDue && p.Reasons[0].Code != ReasonChurnRisk {
			t.Errorf("%s: reasons %+v", p.ID, p.Reasons)
		}
	}
	got := answer(t, m, Query{Product: product, K: MaxPlan, WithinDays: 30})
	checkWindow(t, got.Items, 30)
	want := 0
	for _, d := range due {
		if d < windowEnd(30) {
			want++
		}
	}
	if len(got.Items) != want {
		t.Fatalf("%d customers due within 30 days, want %d", len(got.Items), want)
	}
	if empty := answer(t, m, Query{Product: "no-such-product", WithinDays: 30}); len(empty.Items) != 0 {
		t.Fatalf("an unknown product: %+v", empty.Items)
	}
}

func TestReorderForAllCustomers(t *testing.T) {
	m := scenarioModel(t, "reorder")
	got := answer(t, m, Query{AllCustomers: true, K: MaxPlan, WithinDays: 14})
	if got.For != AllCustomers {
		t.Fatalf("for %q", got.For)
	}
	checkWindow(t, got.Items, 14)
	// Everyone is every salesman's customers together.
	var want []string
	for _, s := range m.Salesmen() {
		want = append(want, ids(answer(t, m, Query{Salesman: s, K: MaxPlan, WithinDays: 14}).Items)...)
	}
	gotIDs := ids(got.Items)
	slices.Sort(gotIDs)
	slices.Sort(want)
	if len(gotIDs) == 0 || !slices.Equal(gotIDs, want) {
		t.Fatalf("all customers %v, want %v", gotIDs, want)
	}
	if few := answer(t, m, Query{AllCustomers: true, K: 3, WithinDays: 14}); len(few.Items) != 3 {
		t.Fatalf("k 3: %d", len(few.Items))
	}
}

func TestReorderWindowRefusals(t *testing.T) {
	cases := []struct {
		model string
		q     Query
		want  string
	}{
		{"visits", Query{Salesman: "R01", WithinDays: 14}, "within_days"},
		{"products", Query{Customer: "C0001", WithinDays: 14}, "within_days"},
		{"reorder", Query{Salesman: "R01", WithinDays: -1}, "within_days"},
		{"reorder", Query{Salesman: "R01", WithinDays: MaxWithinDays + 1}, "within_days"},
		{"products", Query{AllCustomers: true}, "customer"},
		{"reorder", Query{AllCustomers: true, Salesman: "R01"}, "one of"},
		// A reorder model trained without products cannot answer for one.
		{"reorder_customers", Query{Product: "S001", WithinDays: 14}, "products"},
	}
	for _, tc := range cases {
		tc.q.Today = fixtureToday
		_, err := scenarioModel(t, tc.model).Answer(tc.q)
		if !errors.Is(err, ErrBadQuery) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s %+v: %v, want ErrBadQuery mentioning %q", tc.model, tc.q, err, tc.want)
		}
	}
	// A reorder model trained without products still answers its customers.
	old := answer(t, scenarioModel(t, "reorder_customers"), Query{Salesman: "R01", K: MaxPlan, WithinDays: 30})
	checkWindow(t, old.Items, 30)
	if len(old.Items) == 0 || len(old.Items[0].Products) != 0 {
		t.Fatalf("a model without products: %+v", old.Items)
	}
}

func TestCustomerCategoryAndSizeAreKnownAndBucketed(t *testing.T) {
	m := scenarioModel(t, "reorder_peers")
	lo, hi, ok := m.SizeEdges()
	if !ok || !(lo < hi) {
		t.Fatalf("size edges %v %v %v", lo, hi, ok)
	}
	for _, tc := range []struct {
		in, want string
	}{{"Large", "large"}, {" small ", "small"}, {strconv.FormatFloat(lo, 'f', -1, 64), "small"}, {strconv.FormatFloat(hi, 'f', -1, 64), "medium"},
		{strconv.FormatFloat(hi+1, 'f', -1, 64), "large"}, {"Enterprise", "Enterprise"}} {
		if got := m.SizeBucket(tc.in); got != tc.want {
			t.Errorf("SizeBucket(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	// A model trained before sizes has no buckets, and an older one no
	// category or size at all.
	old := scenarioModel(t, "reorder")
	if _, _, ok := old.SizeEdges(); ok || old.SizeBucket("250") != "250" {
		t.Fatal("an older model buckets sizes")
	}
	if c, s := m.CustomerAttrs(m.CustomerIDs()[0]); c == "" || s == "" {
		t.Fatalf("customer attrs %q %q", c, s)
	}
	if c, s := old.CustomerAttrs("nobody"); c != "" || s != "" {
		t.Fatalf("an unknown customer has %q %q", c, s)
	}
}

func TestCustomerListsFilterByCategoryAndSize(t *testing.T) {
	m := scenarioModel(t, "reorder_peers")
	all := answer(t, m, Query{AllCustomers: true, K: MaxPlan})
	count := func(seg, size string) int {
		n := 0
		for _, p := range all.Items {
			if (seg == "" || p.Fields["segment"] == seg) && (size == "" || p.Fields["size"] == size) {
				n++
			}
		}
		return n
	}
	for _, tc := range []struct{ seg, size string }{{"A", ""}, {"", "large"}, {"B", "small"}, {"b", "SMALL"}} {
		got := answer(t, m, Query{AllCustomers: true, K: MaxPlan, Segment: tc.seg, Size: tc.size})
		want := count(strings.ToUpper(tc.seg), strings.ToLower(tc.size))
		if len(got.Items) == 0 || len(got.Items) != want {
			t.Fatalf("%+v: %d customers, want %d", tc, len(got.Items), want)
		}
		for _, p := range got.Items {
			if (tc.seg != "" && !strings.EqualFold(p.Fields["segment"].(string), tc.seg)) || (tc.size != "" && !strings.EqualFold(p.Fields["size"].(string), tc.size)) {
				t.Fatalf("%+v listed %s with %v", tc, p.ID, p.Fields)
			}
		}
	}
	// A number is bucketed as training cut the sizes.
	_, hi, _ := m.SizeEdges()
	big := answer(t, m, Query{AllCustomers: true, K: MaxPlan, Size: strconv.FormatFloat(hi+1, 'f', -1, 64)})
	if len(big.Items) != count("", "large") {
		t.Fatalf("size %v: %d customers, want the %d large ones", hi+1, len(big.Items), count("", "large"))
	}
	// Within a window, for a salesman and for an audience as well.
	within := answer(t, m, Query{AllCustomers: true, K: MaxPlan, WithinDays: 14, Segment: "A", Size: "large"})
	for _, p := range within.Items {
		if p.Fields["segment"] != "A" || p.Fields["size"] != "large" {
			t.Fatalf("window listed %s with %v", p.ID, p.Fields)
		}
	}
	rep := answer(t, m, Query{Salesman: "R01", K: MaxPlan, Segment: "C"})
	for _, p := range rep.Items {
		if p.Fields["segment"] != "C" || p.Fields["salesman"] != "R01" {
			t.Fatalf("salesman listed %s with %v", p.ID, p.Fields)
		}
	}
	a := scenarioModel(t, "audience_peers")
	aud := answer(t, a, Query{Product: a.ProductIDs()[0], K: 50, Size: "small", ExcludeSeen: true})
	if len(aud.Items) == 0 {
		t.Fatal("no small customers in the audience")
	}
	for _, p := range aud.Items {
		if p.Fields["size"] != "small" {
			t.Fatalf("audience listed %s with %v", p.ID, p.Fields)
		}
	}
}

func TestCategoryAndSizeFiltersAreRefusedWhereTheyCannotApply(t *testing.T) {
	noSegment := withHeader(t, scenarioBytes(t, "reorder_customers"), func(h map[string]any) {
		delete(h["customer_attrs"].(map[string]any), "segment")
	})
	cases := []struct {
		name string
		raw  []byte
		q    Query
		want string
	}{
		{"products", scenarioBytes(t, "products_peers"), Query{Customer: "C0001", Segment: "A"}, "customer lists"},
		{"similar", scenarioBytes(t, "similar"), Query{Product: "S001", Size: "large"}, "customer lists"},
		{"no size", scenarioBytes(t, "reorder"), Query{AllCustomers: true, Size: "large"}, "size"},
		{"no category", noSegment, Query{AllCustomers: true, Segment: "A"}, "category"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.q.Today = fixtureToday
			m, err := Parse(tc.raw)
			if err != nil {
				t.Fatal(err)
			}
			_, err = m.Answer(tc.q)
			if !errors.Is(err, ErrBadQuery) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Answer = %v, want ErrBadQuery mentioning %q", err, tc.want)
			}
		})
	}
}

func TestAMalformedSizeEdgeIsRefused(t *testing.T) {
	for _, edges := range []any{[]float64{3}, []float64{5, 2}, []any{1, "x"}} {
		_, err := Parse(withHeader(t, scenarioBytes(t, "reorder_peers"), func(h map[string]any) { h["size_edges"] = edges }))
		if err == nil || (!errors.Is(err, ErrInvalid) && !strings.Contains(err.Error(), "header")) {
			t.Fatalf("size_edges %v: Parse = %v", edges, err)
		}
	}
}

func TestMalformedNearbySettingsAreRefused(t *testing.T) {
	for _, near := range [][2]any{{5.0, 0}, {0, 20}, {0.01, 20}, {501.0, 20}, {5.0, 2}, {5.0, 201}} {
		_, err := Parse(withHeader(t, scenarioBytes(t, "products_near"), func(h map[string]any) { h["near_km"], h["near_peers"] = near[0], near[1] }))
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("near %v: Parse = %v", near, err)
		}
	}
	// Without them (an artifact from before nearby peers) a customer's peers
	// are its whole group, and there is no summary.
	m, err := Parse(withHeader(t, scenarioBytes(t, "products_near"), func(h map[string]any) { delete(h, "near_km"); delete(h, "near_peers") }))
	if err != nil {
		t.Fatal(err)
	}
	res, err := m.Answer(Query{Customer: "A16", K: 10, ExcludeSeen: true, Today: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if res[0].Summary != "" {
		t.Errorf("summary %q", res[0].Summary)
	}
	group := 0
	for _, p := range res[0].Items {
		for _, r := range p.Reasons {
			if r.Code != ReasonPeerGroup {
				continue
			}
			group++
			if !strings.HasSuffix(r.Text, " of 36 Minimarket customers") {
				t.Errorf("%s: %q", p.ID, r.Text)
			}
		}
	}
	if group == 0 {
		t.Error("no peer reasons")
	}
}

// reasonOf is the text of a pick's reason of code, "" for none.
func reasonOf(p Pick, code string) string {
	for _, r := range p.Reasons {
		if r.Code == code {
			return r.Text
		}
	}
	return ""
}

func TestStockGivenWithTheRequestReplacesTheModels(t *testing.T) {
	m := scenarioModel(t, "products_shop")
	// As trained, I4 is out of stock: I5 is offered in its place.
	trained := answer(t, m, Query{Customer: "K01", K: 20, ExcludeSeen: true})
	if slices.Contains(ids(trained.Items), "I4") {
		t.Fatalf("out-of-stock I4 offered: %v", ids(trained.Items))
	}
	i5 := slices.IndexFunc(trained.Items, func(p Pick) bool { return p.ID == "I5" })
	if i5 < 0 || reasonOf(trained.Items[i5], ReasonSubstitute) != "Instead of Indomie 4 (out of stock)" {
		t.Fatalf("I5 not offered instead of I4: %+v", trained.Items)
	}
	// Restocked today, I4 is offered itself and I5 for its own sake.
	restocked := answer(t, m, Query{Customer: "K01", K: 20, ExcludeSeen: true, Stock: map[string]float64{"I4": 30}})
	if !slices.Contains(ids(restocked.Items), "I4") {
		t.Fatalf("restocked I4 not offered: %v", ids(restocked.Items))
	}
	for _, p := range restocked.Items {
		if reasonOf(p, ReasonSubstitute) != "" {
			t.Fatalf("%s offered as a substitute: %v", p.ID, p.Reasons)
		}
	}
	// With the substitute gone as well, neither is.
	gone := answer(t, m, Query{Customer: "K01", K: 20, ExcludeSeen: true, Stock: map[string]float64{"I4": 0, "I5": 0}})
	if slices.Contains(ids(gone.Items), "I4") || slices.Contains(ids(gone.Items), "I5") {
		t.Fatalf("out-of-stock products offered: %v", ids(gone.Items))
	}

	// A model trained before products' details takes stock too, and
	// answers as before without it.
	old := scenarioModel(t, "products_peers")
	before := answer(t, old, Query{Customer: "C0001", K: 10, ExcludeSeen: true})
	first := before.Items[0].ID
	after := answer(t, old, Query{Customer: "C0001", K: 10, ExcludeSeen: true, Stock: map[string]float64{first: 0}})
	if slices.Contains(ids(after.Items), first) || !slices.Equal(ids(after.Items)[:len(after.Items)-1], ids(before.Items)[1:len(after.Items)]) {
		t.Fatalf("stock 0 for %s: %v, before %v", first, ids(after.Items), ids(before.Items))
	}
	aud := scenarioModel(t, "audience_shop")
	if res := answer(t, aud, Query{Product: "I1", K: 5, Stock: map[string]float64{"I1": 0}}); len(res.Items) != 0 {
		t.Fatalf("an out-of-stock product has an audience: %v", ids(res.Items))
	}
	for _, stock := range []map[string]float64{{"I1": math.NaN()}, {"": 3}} {
		if _, err := m.Answer(Query{Customer: "K01", Today: fixtureToday, Stock: stock}); !errors.Is(err, ErrBadQuery) {
			t.Fatalf("stock %v: %v", stock, err)
		}
	}
}

func TestProductListsFilterByBrandAndSubcategory(t *testing.T) {
	m := scenarioModel(t, "products_shop")
	res := answer(t, m, Query{Customer: "K30", K: 20, ExcludeSeen: true, Brand: " indomie "})
	if len(res.Items) == 0 {
		t.Fatal("no Indomie products")
	}
	for _, p := range res.Items {
		if p.Fields["brand"] != "Indomie" {
			t.Fatalf("listed %s with %v", p.ID, p.Fields)
		}
	}
	sim := answer(t, scenarioModel(t, "similar_shop"), Query{Product: "I1", K: 20, Subcategory: "WATER"})
	if len(sim.Items) == 0 {
		t.Fatal("no water like I1")
	}
	for _, p := range sim.Items {
		if p.Fields["subcategory"] != "Water" {
			t.Fatalf("listed %s with %v", p.ID, p.Fields)
		}
	}
	for _, tc := range []struct {
		name string
		m    *Model
		q    Query
		want string
	}{
		{"customer list", scenarioModel(t, "audience_shop"), Query{Product: "I1", Brand: "Indomie"}, "product lists"},
		{"no brand", scenarioModel(t, "products_peers"), Query{Customer: "C0001", Brand: "Acme"}, "brand"},
		{"no subcategory", scenarioModel(t, "similar"), Query{Product: "S001", Subcategory: "x"}, "subcategory"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.q.Today = fixtureToday
			if _, err := tc.m.Answer(tc.q); !errors.Is(err, ErrBadQuery) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Answer = %v, want ErrBadQuery mentioning %q", err, tc.want)
			}
		})
	}
}

func TestProductsAndReordersSuggestAQuantityInPacks(t *testing.T) {
	m := scenarioModel(t, "products_shop")
	res := answer(t, m, Query{Customer: "K02", K: 20})
	suggested := 0
	for _, p := range res.Items {
		if p.Fields["suggested_text"] == "3 cartons (100 pcs)" && p.Fields["suggested_qty"] == 3 {
			suggested++
		}
	}
	if suggested != 2 {
		t.Fatalf("%d products with 3 cartons suggested, want K02's two: %+v", suggested, res.Items)
	}
	// The same model asked when its customers buy again: each due product
	// carries what to order, and an out-of-stock one its substitute.
	raw := withHeader(t, scenarioBytes(t, "products_shop"), func(h map[string]any) { h["scenario"] = ScenarioReorder })
	r, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	due := answer(t, r, Query{AllCustomers: true, K: MaxPlan, WithinDays: 60})
	packs, instead := 0, 0
	for _, p := range due.Items {
		for _, d := range p.Products {
			if d.ID == "I4" {
				t.Fatalf("%s is due for out-of-stock I4", p.ID)
			}
			if d.Fields["instead_of"] == "I4" {
				instead++
			}
			if text, _ := d.Fields["suggested_text"].(string); strings.HasSuffix(text, " pcs)") {
				packs++
			}
		}
	}
	if packs == 0 || instead == 0 {
		t.Fatalf("%d due products with a suggestion, %d instead of I4", packs, instead)
	}
}

func TestProductDetailsHeaderIsChecked(t *testing.T) {
	for _, days := range []any{-1, 366} {
		if _, err := Parse(withHeader(t, scenarioBytes(t, "products_shop"), func(h map[string]any) { h["new_days"] = days })); !errors.Is(err, ErrInvalid) {
			t.Fatalf("new_days %v: Parse = %v", days, err)
		}
	}
	// Without pair_details the two sections are bytes after the last one.
	if _, err := Parse(withHeader(t, scenarioBytes(t, "products_shop"), func(h map[string]any) { delete(h, "pair_details") })); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Parse without pair_details = %v", err)
	}
}

func TestCustomerListsFilterByPriceTierChannelAndRegion(t *testing.T) {
	m := scenarioModel(t, "audience_market")
	res := answer(t, m, Query{Product: "S1", K: MaxK, PriceTier: " gold ", Channel: "RETAIL", Region: "n"})
	if len(res.Items) == 0 {
		t.Fatal("no Gold Retail customers in N")
	}
	for _, p := range res.Items {
		if p.Fields["price_tier"] != "Gold" || p.Fields["channel"] != "Retail" || p.Fields["region"] != "N" {
			t.Fatalf("listed %s with %v", p.ID, p.Fields)
		}
	}
	// Each customer listed carries all three, and its peers are of all.
	all := answer(t, m, Query{Product: "S1", K: 5})
	for _, p := range all.Items {
		if p.Fields["price_tier"] == nil || p.Fields["channel"] == nil || p.Fields["region"] == nil {
			t.Fatalf("%s: fields %v", p.ID, p.Fields)
		}
	}
	for _, tc := range []struct {
		name string
		m    *Model
		q    Query
		want string
	}{
		{"product list", scenarioModel(t, "products_market"), Query{Customer: "Rl16", Region: "N"}, "customer lists"},
		{"no tier", scenarioModel(t, "audience_peers"), Query{Product: "S001", PriceTier: "Gold"}, "price tier"},
		{"no channel", scenarioModel(t, "reorder_peers"), Query{AllCustomers: true, Channel: "Retail"}, "channel"},
		{"no region", scenarioModel(t, "audience_cold"), Query{Product: "S1", Region: "N"}, "region"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.q.Today = fixtureToday
			if _, err := tc.m.Answer(tc.q); !errors.Is(err, ErrBadQuery) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Answer = %v, want ErrBadQuery mentioning %q", err, tc.want)
			}
		})
	}
	if seg, size := m.CustomerAttrs("Rl16"); seg != "Retail" || size != "large" {
		t.Fatalf("CustomerAttrs = %q %q", seg, size)
	}
	if tier, channel, region := m.CustomerMarket("Rl16"); tier != "Gold" || channel != "Retail" || region != "N" {
		t.Fatalf("CustomerMarket = %q %q %q", tier, channel, region)
	}
}

func TestReturnsAndQuotesShapeProductLists(t *testing.T) {
	m := scenarioModel(t, "products_signals")
	// K10 lost a quote for M1 a month ago: it is not offered.
	if res := answer(t, m, Query{Customer: "K10", K: 20, ExcludeSeen: true}); slices.Contains(ids(res.Items), "M1") {
		t.Fatalf("M1 offered after a lost quote: %v", ids(res.Items))
	}
	// K05's open quote is offered with its date.
	res := answer(t, m, Query{Customer: "K05", K: 10, ExcludeSeen: true})
	i := slices.IndexFunc(res.Items, func(p Pick) bool { return p.ID == "M3" })
	if i < 0 || reasonOf(res.Items[i], ReasonQuoted) != "Quoted on 2026-09-20, not ordered yet" {
		t.Fatalf("M3 not offered as quoted: %+v", res.Items)
	}
	// Past the quote window it is neither left out nor offered as quoted.
	later := answer(t, m, Query{Customer: "K05", K: 10, ExcludeSeen: true, Today: fixtureToday.AddDate(0, 0, 90)})
	for _, p := range later.Items {
		if reasonOf(p, ReasonQuoted) != "" {
			t.Fatalf("%s still quoted 90 days on", p.ID)
		}
	}
	aud := scenarioModel(t, "audience_signals")
	if res := answer(t, aud, Query{Product: "M1", K: 40}); slices.Contains(ids(res.Items), "K10") {
		t.Fatalf("K10 in M1's audience after a lost quote: %v", ids(res.Items))
	}
	// A model without returns or quotes answers as before.
	if _, err := Parse(scenarioBytes(t, "products_shop")); err != nil {
		t.Fatal(err)
	}
}

func TestSignalsHeaderIsChecked(t *testing.T) {
	raw := scenarioBytes(t, "products_signals")
	// Without "signals" its four sections are bytes after the last one.
	if _, err := Parse(withHeader(t, raw, func(h map[string]any) { delete(h, "signals") })); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Parse without signals = %v", err)
	}
	if _, err := Parse(withHeader(t, raw, func(h map[string]any) { h["signals"] = -1 })); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Parse with signals -1 = %v", err)
	}
	// The last section is the kinds: one that is no kind is refused, and
	// so is a customer out of range in the first.
	bad := slices.Clone(raw)
	binary.LittleEndian.PutUint32(bad[len(bad)-4:], 9)
	if _, err := Parse(bad); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Parse with kind 9 = %v", err)
	}
	m := scenarioModel(t, "products_signals")
	n := len(m.sc.signals)
	bad = slices.Clone(raw)
	binary.LittleEndian.PutUint32(bad[len(bad)-16*n:], uint32(len(m.users)))
	if _, err := Parse(bad); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Parse with customer %d = %v", len(m.users), err)
	}
}
