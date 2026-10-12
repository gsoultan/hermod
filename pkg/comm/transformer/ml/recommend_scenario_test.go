package ml

import (
	"reflect"
	"testing"
)

func TestRecommendModeForAScenarioSubject(t *testing.T) {
	reg := &fakeRecommender{items: []map[string]any{{"id": "C1", "score": 0.7, "rank": 1, "plan_date": "2026-10-13"}}}
	out, err := run(t, reg, "tenant-a", map[string]any{
		"model": "plan", "mode": "recommend", "recommendFor": "salesman", "idField": "rep", "periodDays": "5", "k": 12,
		"rules": `[{"kind":"capacity","per_day":4}]`,
	}, map[string]any{"rep": "R07"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"for": map[string]any{"salesman": "R07"}, "period_days": 5, "k": 12,
		"rules": []any{map[string]any{"kind": "capacity", "per_day": 4.0}}}
	if !reflect.DeepEqual(reg.req, want) {
		t.Errorf("request = %v, want %v", reg.req, want)
	}
	if got := out.Data()[defaultRecommendField]; !reflect.DeepEqual(got, reg.items) {
		t.Errorf("recommendations = %v", got)
	}
	for _, to := range []string{"customer", "product"} {
		reg := &fakeRecommender{}
		if _, err := run(t, reg, "", map[string]any{"model": "m", "mode": "recommend", "recommendFor": to, "idField": "id",
			"excludeSeen": "false"}, map[string]any{"id": 7.0}); err != nil {
			t.Fatal(err)
		}
		if want := map[string]any{"for": map[string]any{to: 7.0}, "exclude_seen": false}; !reflect.DeepEqual(reg.req, want) {
			t.Errorf("%s: request = %v, want %v", to, reg.req, want)
		}
	}
	for _, cfg := range []map[string]any{
		{"recommendFor": "user", "periodDays": 7},
		{"recommendFor": "salesman", "periodDays": 400},
		{"recommendFor": "item", "rules": "[]"},
		{"recommendFor": "salesman", "rules": "{not json"},
	} {
		cfg["model"], cfg["mode"], cfg["idField"] = "m", "recommend", "id"
		if _, err := run(t, &fakeRecommender{}, "", cfg, map[string]any{"id": "x"}); err == nil {
			t.Errorf("%v: accepted", cfg)
		}
	}
}
