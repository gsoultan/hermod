package recommend

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"slices"
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
