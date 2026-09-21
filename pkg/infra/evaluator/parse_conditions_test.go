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

// TestParseConditionsAcceptsTheArrayTheEditorSaves pins the shape a UI-built
// node actually carries.
//
// ConditionConfig.tsx passes FilterEditor's Condition[] straight to
// updateNodeConfig, which merges it into node.data without stringifying, so
// `conditions` reaches the engine as []any -- not the JSON string
// FilterDataConfig.tsx writes. Reading only the string form yielded an empty
// list, and an empty list is not an error to EvaluateConditions: it returns
// true. Every condition node built in the editor took its "true" branch on
// every message, whatever the user had configured.
func TestParseConditionsAcceptsTheArrayTheEditorSaves(t *testing.T) {
	want := []map[string]any{
		{"field": "status", "operator": "eq", "value": "active"},
		{"field": "amount", "operator": "gt", "value": "100"},
	}

	for _, tc := range []struct {
		name string
		raw  any
	}{
		{"[]any of map, as JSONB decodes it", []any{
			map[string]any{"field": "status", "operator": "eq", "value": "active"},
			map[string]any{"field": "amount", "operator": "gt", "value": "100"},
		}},
		{"[]map[string]any, as a Go caller builds it", []map[string]any{
			{"field": "status", "operator": "eq", "value": "active"},
			{"field": "amount", "operator": "gt", "value": "100"},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseConditions(map[string]any{"conditions": tc.raw})
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("ParseConditions = %#v, want %#v", got, want)
			}
		})
	}
}

// TestParseObjectListIgnoresUnusableShapes: a config value that is neither a
// JSON string nor a list of objects yields no entries rather than a partial
// list, so a caller cannot half-apply a malformed config.
func TestParseObjectListIgnoresUnusableShapes(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  any
	}{
		{"nil", nil},
		{"empty string", ""},
		{"unparseable string", "not json"},
		{"number", 42},
		{"map, not a list", map[string]any{"field": "status"}},
		{"list of scalars", []any{"status", "amount"}},
		{"empty list", []any{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParseObjectList(tc.raw); got != nil {
				t.Fatalf("ParseObjectList(%#v) = %#v, want nil", tc.raw, got)
			}
		})
	}
}

// TestParseObjectListArrayResultIsNotShared holds the array path to the same
// rule the string path already has: a caller must not be able to mutate what
// another caller reads.
func TestParseObjectListArrayResultIsNotShared(t *testing.T) {
	src := []map[string]any{{"field": "status", "operator": "eq", "value": "active"}}

	first := ParseObjectList(src)
	first[0] = map[string]any{"field": "mutated"}

	second := ParseObjectList(src)
	if second[0]["field"] != "status" {
		t.Errorf("second parse saw field %q; an earlier caller's slice write reached it", second[0]["field"])
	}
}
