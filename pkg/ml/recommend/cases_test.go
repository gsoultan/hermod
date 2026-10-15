package recommend

import (
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The recommendation cases of docs/ml-recommendation-cases.md, served from
// the models the worker trained on them (testdata/gen_scenario_fixtures.py).

func TestTheIndustrialSuppliersAccountsDueThisMonth(t *testing.T) {
	m := scenarioModel(t, "reorder_industrial")
	got := answer(t, m, Query{AllCustomers: true, K: MaxPlan, WithinDays: 30})
	checkWindow(t, got.Items, 30)
	overdue := 0
	for _, p := range got.Items {
		if p.Overdue {
			overdue++
		}
		// Long cycles: "Usually orders every 90 days; last order 133 days ago".
		text := p.Reasons[0].Text
		days, err := strconv.Atoi(strings.Fields(strings.TrimPrefix(text, "Usually orders every "))[0])
		if err != nil || days < 30 {
			t.Errorf("%s: %q", p.ID, text)
		}
	}
	if overdue == 0 || overdue == len(got.Items) {
		t.Fatalf("%d of %d overdue; the case has both", overdue, len(got.Items))
	}
	// One price tier's accounts are those of the whole list on that tier.
	gold := answer(t, m, Query{AllCustomers: true, K: MaxPlan, WithinDays: 30, PriceTier: "contract a"})
	var want []string
	for _, p := range got.Items {
		if p.Fields["price_tier"] == "Contract A" {
			want = append(want, p.ID)
		}
	}
	if len(want) == 0 || !slices.Equal(ids(gold.Items), want) {
		t.Fatalf("Contract A accounts due %v, want %v", ids(gold.Items), want)
	}
}

func TestTheIndustrialSuppliersQuotesShapeItsOffers(t *testing.T) {
	m := scenarioModel(t, "products_industrial")
	// ACC02 lost a quote for IN062 last month; ACC04 was quoted IN002 and
	// has not ordered it yet.
	if got := ids(answer(t, m, Query{Customer: "ACC02", K: 20, ExcludeSeen: true}).Items); slices.Contains(got, "IN062") {
		t.Errorf("ACC02 is offered IN062, the quote it lost: %v", got)
	}
	open := answer(t, m, Query{Customer: "ACC04", K: 10, ExcludeSeen: true})
	i := slices.IndexFunc(open.Items, func(p Pick) bool { return p.ID == "IN002" })
	if i < 0 || !slices.Contains(open.Items[i].Reasons, Reason{Code: ReasonQuoted, Text: "Quoted on 2026-09-17, not ordered yet"}) {
		t.Fatalf("ACC04's offers %+v, want IN002 quoted on 2026-09-17", open.Items)
	}
}

func TestThePharmacyChainOffersTheGenericOfABrandOutOfStock(t *testing.T) {
	m := scenarioModel(t, "similar_pharmacy")
	for brand, generic := range map[string]string{"PH001": "PH002", "PH003": "PH004", "PH005": "PH006"} {
		got := answer(t, m, Query{Product: brand, K: 5})
		if len(got.Items) == 0 || got.Items[0].ID != generic {
			t.Fatalf("like %s: %v, want %s first", brand, ids(got.Items), generic)
		}
		if !slices.ContainsFunc(got.Items[0].Reasons, func(r Reason) bool { return strings.HasPrefix(r.Text, "Same subcategory (") }) {
			t.Errorf("like %s: %s's reasons %+v", brand, generic, got.Items[0].Reasons)
		}
	}
}
