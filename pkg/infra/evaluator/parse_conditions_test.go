package evaluator

// ParseConditions runs once per message: ConditionNode.Execute calls it before
// evaluating anything. For a node that stores its conditions as a JSON string
// — which is what the editor writes for anything past a single rule — that is
// a json.Unmarshal of the same bytes, per message, for the life of the
// workflow. The result is the same every time.

import (
	"reflect"
	"testing"
)

func conditionsJSON() map[string]any {
	return map[string]any{
		"conditions": `[{"field":"status","operator":"eq","value":"active"},` +
			`{"field":"amount","operator":"gt","value":"100"}]`,
	}
}

func conditionsTriple() map[string]any {
	return map[string]any{
		"field":    "status",
		"operator": "eq",
		"value":    "active",
	}
}

// TestParseConditionsShapes pins what each config shape produces, so a cache
// cannot quietly change it.
func TestParseConditionsShapes(t *testing.T) {
	cases := []struct {
		name   string
		config map[string]any
		want   []map[string]any
	}{
		{"empty config", map[string]any{}, nil},
		{"json list", conditionsJSON(), []map[string]any{
			{"field": "status", "operator": "eq", "value": "active"},
			{"field": "amount", "operator": "gt", "value": "100"},
		}},
		{"single triple", conditionsTriple(), []map[string]any{
			{"field": "status", "operator": "eq", "value": "active"},
		}},
		{"triple with no field", map[string]any{"operator": "eq", "value": "x"}, nil},
		{"malformed json falls back to the triple", map[string]any{
			"conditions": `[{"field":`,
			"field":      "f", "operator": "eq", "value": "v",
		}, []map[string]any{{"field": "f", "operator": "eq", "value": "v"}}},
		{"empty json list falls back to the triple", map[string]any{
			"conditions": `[]`,
			"field":      "f", "operator": "eq", "value": "v",
		}, []map[string]any{{"field": "f", "operator": "eq", "value": "v"}}},
		{"json list wins over the triple", map[string]any{
			"conditions": `[{"field":"a","operator":"eq","value":"1"}]`,
			"field":      "ignored", "operator": "eq", "value": "x",
		}, []map[string]any{{"field": "a", "operator": "eq", "value": "1"}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseConditions(tc.config)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ParseConditions(%v) = %#v, want %#v", tc.config, got, tc.want)
			}
		})
	}
}

// TestParseConditionsResultIsNotShared: whatever caching happens, a caller
// must not be able to reach into another caller's copy. EvaluateConditions
// only reads today, but handing out shared mutable state is how that stops
// being true.
func TestParseConditionsResultIsNotShared(t *testing.T) {
	cfg := conditionsJSON()

	first := ParseConditions(cfg)
	if len(first) == 0 {
		t.Fatal("precondition: no conditions parsed")
	}
	first[0]["field"] = "mutated"
	first[0]["injected"] = true

	second := ParseConditions(cfg)
	if second[0]["field"] != "status" {
		t.Errorf(`second parse saw field %q; a mutation by an earlier caller reached it`, second[0]["field"])
	}
	if _, ok := second[0]["injected"]; ok {
		t.Error("second parse saw a key injected by an earlier caller")
	}
}

// TestParseConditionsDoesNotReparsePerCall is the point of the change.
func TestParseConditionsDoesNotReparsePerCall(t *testing.T) {
	cfg := conditionsJSON()
	ParseConditions(cfg) // warm

	allocs := testing.AllocsPerRun(200, func() { ParseConditions(cfg) })
	if allocs > 10 {
		t.Errorf("ParseConditions allocates %v times per call on a cached config; the JSON is being re-parsed every message", allocs)
	}
}
