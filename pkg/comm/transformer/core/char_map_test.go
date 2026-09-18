package core

import (
	"testing"

	"github.com/gsoultan/hermod/pkg/comm/message"
)

func charMap(t *testing.T, in any, cfg map[string]any) any {
	t.Helper()
	m := message.AcquireMessage()
	m.SetData("in", in)
	if _, ok := cfg["field"]; !ok {
		cfg["field"] = "in"
	}
	if _, ok := cfg["targetField"]; !ok {
		cfg["targetField"] = "out"
	}
	tr := &CharMapTransformer{}
	out, err := tr.Transform(t.Context(), m, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return out.Data()["out"]
}

// The editor writes the chosen operation under `op`
// (ui/src/components/workflow/Transformation/configs/data/CharMapConfig.tsx).
// The node read `operations` and `operation` and never `op`, so the operation
// list came out empty, the loop did nothing, and every Character Map node
// copied its field through untouched -- a green node, no error, no log line.
// The editor is the only way to build one of these, so this was every node.
func TestCharMap_OperationKeyTheEditorWrites(t *testing.T) {
	if got := charMap(t, "hello", map[string]any{"op": "uppercase"}); got != "HELLO" {
		t.Errorf("op=uppercase gave %#v, want \"HELLO\"", got)
	}
}

func TestCharMap_OperationKeys(t *testing.T) {
	cases := []struct {
		name string
		cfg  map[string]any
		in   any
		want any
	}{
		{"op", map[string]any{"op": "lowercase"}, "HELLO", "hello"},
		{"operation", map[string]any{"operation": "lowercase"}, "HELLO", "hello"},
		{"operations list", map[string]any{"operations": []any{"lowercase"}}, "HELLO", "hello"},

		// The list is applied in order, which is the reason it is a list.
		{"operations chain", map[string]any{"operations": []any{"trim", "uppercase"}}, "  hi  ", "HI"},

		// A config that carries more than one of the keys resolves the same way
		// every time: the list is the most specific, then `operation`, then the
		// editor's `op`. Without a fixed order a re-saved node could change
		// behaviour on a key it was not edited to touch.
		{"list wins over operation", map[string]any{"operations": []any{"uppercase"}, "operation": "lowercase"}, "hi", "HI"},
		{"operation wins over op", map[string]any{"operation": "uppercase", "op": "lowercase"}, "hi", "HI"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := charMap(t, tc.in, tc.cfg); got != tc.want {
				t.Errorf("got %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestCharMap_EveryOperationTheEditorOffers(t *testing.T) {
	// Each value here is one the editor's Operation select can write. An entry
	// the node does not implement is a no-op the operator cannot see.
	cases := []struct {
		op   string
		in   string
		want string
	}{
		{"uppercase", "hi", "HI"},
		{"lowercase", "HI", "hi"},
		{"trim", "  hi  ", "hi"},
		{"trim_left", "  hi  ", "hi  "},
		{"trim_right", "  hi  ", "  hi"},
	}
	for _, tc := range cases {
		t.Run(tc.op, func(t *testing.T) {
			if got := charMap(t, tc.in, map[string]any{"op": tc.op}); got != tc.want {
				t.Errorf("%s(%q) = %#v, want %#v", tc.op, tc.in, got, tc.want)
			}
		})
	}
}
