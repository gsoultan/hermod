package registry

import (
	"encoding/json"
	"maps"
	"testing"

	"github.com/gsoultan/Hermod/internal/storage"
	"github.com/gsoultan/Hermod/pkg/comm/message"
	// Transformers register themselves in init(), so the test binary has to
	// link the packages it exercises. cmd/hermod imports both; without these a
	// perfectly good transformation looks unregistered.
	_ "github.com/gsoultan/Hermod/pkg/comm/transformer/advanced"
	_ "github.com/gsoultan/Hermod/pkg/comm/transformer/core"
	_ "github.com/gsoultan/Hermod/pkg/comm/transformer/security"
)

// ---------------------------------------------------------------------------
// Per-transformation-type behaviour.
//
// advanced_transformations_e2e and comprehensive_transformations_e2e exercised
// eleven transformation types through the browser. Go tests covered six of
// them, so char_map, data_conversion, fuzzy_lookup, lua, mask and
// parallel_pipeline had no coverage outside a pair of specs that no longer run
// — including mask, which is the PII control, and lua, which executes a script.
//
// These go through applyTransformation rather than the simulation endpoint, so
// a failure names the transformation rather than the workflow around it.
// api_lookup and db_lookup are deliberately absent: they need an HTTP endpoint
// and a database respectively, so they belong with the integration-tagged tests
// rather than here.
// ---------------------------------------------------------------------------

// transform runs one transformation over a message and returns the resulting
// payload, so each case below reads as input -> config -> output.
func transform(t *testing.T, reg *Registry, in map[string]any, cfg map[string]any) map[string]any {
	t.Helper()

	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshalling input: %v", err)
	}

	msg := message.AcquireMessage()
	// Both representations. Transformers are split between them: the CDC-shaped
	// ones read the after-image, while others (mask among them) read Data(),
	// which lazily unmarshals the payload and never looks at After. Setting one
	// and asserting on the other makes a working transformation look like a
	// no-op.
	msg.SetAfter(raw)
	msg.SetPayload(raw)

	transType, _ := cfg["transType"].(string)
	out, err := reg.applyTransformation(t.Context(), msg, transType, cfg)
	if err != nil {
		msg.Release()
		t.Fatalf("%s: %v", transType, err)
	}
	if out == nil {
		msg.Release()
		t.Fatalf("%s returned no message", transType)
	}
	t.Cleanup(out.Release)

	// Read back from whichever representation the transformer wrote to.
	got := map[string]any{}
	if raw := out.After(); len(raw) > 0 {
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("%s produced an unparseable after-image %q: %v", transType, raw, err)
		}
	}
	maps.Copy(got, out.Data())
	return got
}

func TestCharMapUppercasesIntoATargetField(t *testing.T) {
	reg := newSimRegistry(t)

	got := transform(t, reg,
		map[string]any{"name": "john doe"},
		map[string]any{
			"transType":   "char_map",
			"field":       "name",
			"operations":  []any{"uppercase"},
			"targetField": "name_upper",
		})

	if got["name_upper"] != "JOHN DOE" {
		t.Errorf("name_upper = %v, want JOHN DOE", got["name_upper"])
	}
	// A targetField means the original must survive; overwriting it would lose
	// the source value the rest of the pipeline may still need.
	if got["name"] != "john doe" {
		t.Errorf("source field was modified: name = %v, want it unchanged", got["name"])
	}
}

func TestDataConversionWritesTheConvertedType(t *testing.T) {
	reg := newSimRegistry(t)

	got := transform(t, reg,
		map[string]any{"id": 42},
		map[string]any{
			"transType":   "data_conversion",
			"field":       "id",
			"targetType":  "string",
			"targetField": "id_str",
		})

	s, ok := got["id_str"].(string)
	if !ok {
		t.Fatalf("id_str is %T (%v), want a string: a sink column typed TEXT would "+
			"reject a number", got["id_str"], got["id_str"])
	}
	if s != "42" {
		t.Errorf("id_str = %q, want \"42\"", s)
	}
}

// TestMaskRedactsAnEmail is the PII control. A mask that silently does nothing
// is the worst outcome here: the pipeline reports success and the unmasked
// value lands in the destination.
func TestMaskRedactsAnEmail(t *testing.T) {
	reg := newSimRegistry(t)

	const original = "john@example.com"
	got := transform(t, reg,
		map[string]any{"email": original},
		map[string]any{"transType": "mask", "field": "email", "maskType": "email"})

	masked, _ := got["email"].(string)
	if masked == original {
		t.Fatalf("email came through unmasked as %q; the transformation reported success "+
			"and wrote the raw address to the destination", masked)
	}
	if masked == "" {
		t.Error("email was emptied rather than masked, which destroys the record " +
			"instead of protecting it")
	}
}

func TestFuzzyLookupMatchesTheNearestOption(t *testing.T) {
	reg := newSimRegistry(t)

	got := transform(t, reg,
		map[string]any{"name_upper": "JOHN DOE"},
		map[string]any{
			"transType":   "fuzzy_lookup",
			"field":       "name_upper",
			"options":     []any{"JON DOE", "JANE SMITH"},
			"threshold":   0.5,
			"targetField": "name_fuzzy",
		})

	if got["name_fuzzy"] != "JON DOE" {
		t.Errorf("name_fuzzy = %v, want JON DOE (the nearer of the two options)", got["name_fuzzy"])
	}
}

func TestLuaScriptCanSetAField(t *testing.T) {
	reg := newSimRegistry(t)

	got := transform(t, reg,
		map[string]any{"name": "Doe"},
		map[string]any{
			"transType": "lua",
			"script":    `msg.name = "MR " .. (msg.name or ""); msg.e2e = true`,
		})

	if got["name"] != "MR Doe" {
		t.Errorf("name = %v, want \"MR Doe\"", got["name"])
	}
	if got["e2e"] != true {
		t.Errorf("e2e = %v, want true", got["e2e"])
	}
}

// TestUnknownTransformationIsRejected keeps a typo from becoming a silent
// pass-through: a workflow referencing a transformation that does not exist
// must fail rather than quietly forward the message untouched.
func TestUnknownTransformationIsRejected(t *testing.T) {
	reg := newSimRegistry(t)

	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetAfter([]byte(`{"k":"v"}`))

	out, err := reg.applyTransformation(t.Context(), msg,
		"no_such_transformation", map[string]any{"transType": "no_such_transformation"})
	if err == nil {
		if out != nil && out != msg {
			out.Release()
		}
		t.Error("an unknown transformation type was accepted; a typo in a workflow " +
			"would forward messages untransformed and report success")
	}
}

// TestParallelPipelineAppliesEveryStep covers the one transformation type the
// retired browser specs exercised that nothing in Go reached.
//
// parallel_pipeline is a distinct code path from pipeline: the node executor
// routes them to different functions, and the parallel one fans the message out
// to a clone per step and runs them concurrently. node_ownership_test.go has a
// case named "parallel-pipeline" but it configures transType "pipeline", so the
// concurrent path was never executed.
//
// A step quietly dropped here is invisible: the message arrives at the sink
// missing a field, with no error anywhere.
func TestParallelPipelineAppliesEveryStep(t *testing.T) {
	reg := newSimRegistry(t)

	node := &storage.WorkflowNode{
		ID:   "par",
		Type: "transformation",
		Config: map[string]any{
			"transType": "parallel_pipeline",
			"steps": `[{"transType":"set","column.a":"'1'"},` +
				`{"transType":"set","column.b":"'2'"},` +
				`{"transType":"set","column.c":"'3'"}]`,
		},
	}

	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetAfter([]byte(`{"k":"v"}`))
	msg.SetPayload([]byte(`{"k":"v"}`))

	out, _, err := reg.RunWorkflowNode("wf-parallel", node, msg)
	if err != nil {
		t.Fatalf("parallel_pipeline: %v", err)
	}
	t.Cleanup(func() {
		for _, m := range out {
			if m != msg {
				m.Release()
			}
		}
	})
	if len(out) == 0 {
		t.Fatal("parallel_pipeline produced no messages; every branch was lost")
	}

	// Each step runs on its own clone, so the fields land across the returned
	// messages rather than all on one. What must not happen is a step vanishing.
	seen := map[string]bool{}
	for _, m := range out {
		got := map[string]any{}
		if raw := m.After(); len(raw) > 0 {
			_ = json.Unmarshal(raw, &got)
		}
		maps.Copy(got, m.Data())
		for _, f := range []string{"a", "b", "c"} {
			if _, ok := got[f]; ok {
				seen[f] = true
			}
		}
		if _, ok := got["k"]; !ok {
			t.Errorf("a branch lost the original field k: %v", got)
		}
	}

	for _, f := range []string{"a", "b", "c"} {
		if !seen[f] {
			t.Errorf("no branch produced field %q across %d message(s); a parallel step "+
				"was dropped without reporting an error", f, len(out))
		}
	}
}
