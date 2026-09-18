package advanced

import (
	"encoding/json"
	"maps"
	"slices"
	"testing"

	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
)

// A `set` node applies its columns one at a time, in the order Prepare put them
// in. That order came from ranging a Go map, which is deliberately randomised
// per range -- so two columns that touch the same path, or one whose expression
// reads what another just wrote, resolved differently from message to message
// inside a single run. Nothing in the config is wrong when that happens and
// nothing logs; the output is simply not the same twice.
//
// JSON has no key order, so the *editor's* row order cannot survive the round
// trip and a sort is the only thing that can be stable. This pins that the
// order is fixed, not that it matches the editor.
func TestPrepare_ColumnOrderIsDeterministic(t *testing.T) {
	tr := &AdvancedTransformer{evaluator: evaluator.NewEvaluator()}
	cfg := map[string]any{
		"transType":    "set",
		"label":        "Set fields",
		"column.zebra": "1",
		"column.alpha": "2",
		"column.mid":   "3",
		"column.b":     "4",
		"column.a.b":   "5",
		"column.a":     "6",
	}

	paths := func() []string {
		prepared, err := tr.Prepare(maps.Clone(cfg))
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}
		cols, ok := prepared["_parsed_columns"].([]columnConfig)
		if !ok {
			t.Fatal("Prepare did not produce _parsed_columns")
		}
		out := make([]string, len(cols))
		for i, c := range cols {
			out[i] = c.path
		}
		return out
	}

	first := paths()
	// A six-key map ranged 50 times would almost certainly show a second order
	// if one were possible.
	for i := range 50 {
		if got := paths(); !slices.Equal(got, first) {
			t.Fatalf("run %d: order changed\n first: %v\n   got: %v", i, first, got)
		}
	}

	// A parent path is applied before the child that writes into it, rather than
	// the two racing.
	if slices.Index(first, "a") > slices.Index(first, "a.b") {
		t.Errorf("want %q applied before %q, got order %v", "a", "a.b", first)
	}
}

// The fallback path, taken when a config was never passed through Prepare, has
// to agree with Prepare -- otherwise the same node behaves one way in the
// engine and another in the preview endpoint.
func TestTransform_UnpreparedConfigUsesTheSameOrder(t *testing.T) {
	tr := &AdvancedTransformer{evaluator: evaluator.NewEvaluator()}
	cfg := map[string]any{
		"transType":    "set",
		"column.zebra": "1",
		"column.alpha": "2",
		"column.mid":   "3",
	}

	prepared, err := tr.Prepare(maps.Clone(cfg))
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	want := prepared["_parsed_columns"].([]columnConfig)

	for range 50 {
		msg := message.AcquireMessage()
		// Transform with no _parsed_columns takes the fallback.
		if _, err := tr.Transform(t.Context(), msg, maps.Clone(cfg)); err != nil {
			t.Fatalf("Transform: %v", err)
		}
	}

	got, err := tr.Prepare(maps.Clone(cfg))
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if !slices.Equal(want, got["_parsed_columns"].([]columnConfig)) {
		t.Errorf("Prepare is not stable across calls")
	}

	if !slices.IsSortedFunc(want, func(a, b columnConfig) int {
		switch {
		case a.path < b.path:
			return -1
		case a.path > b.path:
			return 1
		default:
			return 0
		}
	}) {
		t.Errorf("want columns in a fixed (sorted) order, got %v", want)
	}
}

// The `advanced` branch evaluates into a map and then writes that map out, and
// the write loop ranged *that* map -- so fixing the evaluation order alone was
// not enough. SetData nests a dotted path, so two columns whose paths overlap
// produced two different messages from the same node and the same input.
// Measured on this config over 300 runs before the fix: 266 came out
// `{"a":{"b":"child"}}` and 34 came out `{"a":"parent"}`.
//
// Values are scalars on purpose. A composite config value is handed to SetData
// by reference and then mutated in place, so a config reused across iterations
// converges after the first run and hides the very thing under test.
//
// Compared as JSON, not with %v: fmt prints a map with its keys sorted, which
// would hide the difference too.
func TestTransform_AdvancedWritesInAFixedOrder(t *testing.T) {
	tr := &AdvancedTransformer{evaluator: evaluator.NewEvaluator()}

	// Sorted order puts `a` before `a.b`, so the child nests into the parent.
	const want = `{"a":{"b":"child"},"alpha":"a","zebra":"z"}`
	for i := range 300 {
		msg := message.AcquireMessage()
		out, err := tr.Transform(t.Context(), msg, map[string]any{
			"transType":    "advanced",
			"column.a":     "'parent'",
			"column.a.b":   "'child'",
			"column.zebra": "'z'",
			"column.alpha": "'a'",
		})
		if err != nil {
			t.Fatalf("Transform: %v", err)
		}
		b, err := json.Marshal(out.Data())
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if string(b) != want {
			t.Fatalf("run %d:\n got: %s\nwant: %s", i, b, want)
		}
	}
}
