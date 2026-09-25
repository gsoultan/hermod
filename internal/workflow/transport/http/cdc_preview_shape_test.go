package http

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/engine/registry"
	// Node types and transformers the way cmd/hermod links them. Without them a
	// condition passes every message through unrouted, and a transformation node
	// is a pass-through too, so a test could not tell a node that ran from one
	// that did nothing.
	_ "github.com/gsoultan/hermod/internal/engine/registry/nodes"
	_ "github.com/gsoultan/hermod/pkg/comm/transformer/advanced"
	_ "github.com/gsoultan/hermod/pkg/comm/transformer/core"
)

// A CDC sample posted to the preview endpoint comes back with its after-image
// under "after" and its system fields at the root -- the shape a real CDC
// message has. It used to come back with the system fields in *both* places:
//
//	{"operation":"create","table":"orders",
//	 "after":{"operation":"create","table":"orders","user_id":1}}
//
// because populateMessageFromMap copied operation/table/schema into the data map
// "for convenience in transformations", and ToMap marshals the whole data map as
// the after-image when there is no payload. A live CDC source sets the payload
// (SetAfter), so only messages built field-by-field -- which is to say, only the
// preview -- ever showed this.
//
// The convenience was never needed: evaluator.GetMsgValByPath exposes
// operation/op/table/schema as virtual fields resolved from the message itself,
// and deliberately lets a real data column of the same name win over them.
func TestPreview_CDCSampleDoesNotEchoSystemFieldsIntoAfter(t *testing.T) {
	sample := map[string]any{
		"operation": "create",
		"table":     "orders",
		"schema":    "public",
		"after":     map[string]any{"user_id": 1, "amount": 10},
	}

	resp := postTransformation(t, map[string]any{
		"field":       "user_id",
		"mapping":     `{"1":"ada"}`,
		"mappingType": "exact",
		"targetField": "owner",
	}, "mapping", sample)

	// The root still describes the change.
	for field, want := range map[string]any{"operation": "create", "table": "orders", "schema": "public"} {
		if got := resp[field]; got != want {
			t.Errorf("root %q = %#v, want %#v (response: %#v)", field, got, want, resp)
		}
	}

	after, ok := resp["after"].(map[string]any)
	if !ok {
		t.Fatalf("after is %T, want an object (response: %#v)", resp["after"], resp)
	}

	// The after-image is the row, not the row plus a copy of the envelope.
	for _, field := range []string{"operation", "table", "schema"} {
		if _, echoed := after[field]; echoed {
			t.Errorf("after[%q] = %#v -- the envelope was copied into the after-image; "+
				"the panel shows every system field twice (after: %#v)", field, after[field], after)
		}
	}

	// And the row itself, including what the transformation wrote, is intact.
	if after["user_id"] != float64(1) {
		t.Errorf("after[\"user_id\"] = %#v, want 1 (after: %#v)", after["user_id"], after)
	}
	if after["owner"] != "ada" {
		t.Errorf("after[\"owner\"] = %#v, want %q -- the transformation's output is missing (after: %#v)",
			after["owner"], "ada", after)
	}
}

// The virtual fields are what makes the change above safe: a transformation
// addressing "table" or "operation" must still resolve, even though those are no
// longer copied into the data map.
func TestPreview_CDCSystemFieldsAreStillAddressable(t *testing.T) {
	sample := map[string]any{
		"operation": "update",
		"table":     "orders",
		"after":     map[string]any{"user_id": 1},
	}

	resp := postTransformation(t, map[string]any{
		"field":       "operation",
		"mapping":     `{"update":"changed"}`,
		"mappingType": "exact",
		"targetField": "kind",
	}, "mapping", sample)

	after, ok := resp["after"].(map[string]any)
	if !ok {
		t.Fatalf("after is %T, want an object (response: %#v)", resp["after"], resp)
	}
	if after["kind"] != "changed" {
		t.Errorf("kind = %#v, want %q -- a transformation could not read the operation (response: %#v)",
			after["kind"], "changed", resp)
	}
}

// A CDC row is free to have a column called "table". It is data, and it must
// survive -- both in the after-image and when a transformation reads it, where
// it outranks the virtual field of the same name.
func TestPreview_ADataColumnNamedTableOutranksTheVirtualField(t *testing.T) {
	sample := map[string]any{
		"operation": "create",
		"table":     "bookings",
		"after":     map[string]any{"table": "corner-booth", "covers": 4},
	}

	resp := postTransformation(t, map[string]any{
		"field":       "table",
		"mapping":     `{"corner-booth":"window"}`,
		"mappingType": "exact",
		"targetField": "moved_to",
	}, "mapping", sample)

	if resp["table"] != "bookings" {
		t.Errorf("root table = %#v, want %q (response: %#v)", resp["table"], "bookings", resp)
	}

	after, ok := resp["after"].(map[string]any)
	if !ok {
		t.Fatalf("after is %T, want an object (response: %#v)", resp["after"], resp)
	}
	if after["table"] != "corner-booth" {
		t.Errorf("after[\"table\"] = %#v, want %q -- the data column was lost or overwritten by the "+
			"message's table name (after: %#v)", after["table"], "corner-booth", after)
	}
	if after["moved_to"] != "window" {
		t.Errorf("moved_to = %#v, want %q -- the transformation read the message's table name instead "+
			"of the data column (after: %#v)", after["moved_to"], "window", after)
	}
}

// ---------------------------------------------------------------------------
// The workflow preview: POST /api/workflows/test.
//
// Refreshing AVAILABLE FIELDS re-runs this so every node can read what the
// node before it emits. It could not do that for a workflow that has no sink
// yet, and on a workflow with two sources it fed one sample to both, so a
// refresh on one branch put its columns on the other.
// ---------------------------------------------------------------------------

func postSimulation(t *testing.T, body map[string]any) (int, []map[string]any, string) {
	t.Helper()
	store := newSQLiteStore(t, "simulate")
	h := &WorkflowHandler{Handler: &handlers.Handler{Storage: store, Registry: registry.NewRegistry(store)}}

	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/workflows/test", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.TestWorkflow(rec, req)

	var steps []map[string]any
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &steps); err != nil {
			t.Fatalf("decode steps %q: %v", rec.Body.String(), err)
		}
	}
	return rec.Code, steps, rec.Body.String()
}

func stepPayload(steps []map[string]any, nodeID string) map[string]any {
	for _, s := range steps {
		if s["node_id"] == nodeID {
			if p, ok := s["payload"].(map[string]any); ok {
				return p
			}
		}
	}
	return nil
}

func TestSimulationEndpointPreviewsEachBranchFromItsOwnSample(t *testing.T) {
	workflow := map[string]any{
		"name": "two branches, no sink yet",
		"nodes": []map[string]any{
			{"id": "src-a", "type": "source", "ref_id": "orders"},
			{"id": "src-b", "type": "source", "ref_id": "customers"},
			{"id": "ta", "type": "transformation", "config": map[string]any{"transType": "set", "column.branch": "'a'"}},
			{"id": "tb", "type": "transformation", "config": map[string]any{"transType": "set", "column.branch": "'b'"}},
		},
		"edges": []map[string]any{
			{"id": "ea", "source_id": "src-a", "target_id": "ta"},
			{"id": "eb", "source_id": "src-b", "target_id": "tb"},
		},
	}

	code, steps, body := postSimulation(t, map[string]any{
		"workflow": workflow,
		"messages": map[string]any{
			"src-a": map[string]any{"only_in_a": "A"},
			"src-b": map[string]any{"only_in_b": "B"},
		},
		"partial": true,
	})
	if code != http.StatusOK {
		t.Fatalf("a partial preview of a workflow with no sink yet returned %d: %s", code, body)
	}
	for node, want := range map[string]struct{ has, lacks string }{
		"ta": {"only_in_a", "only_in_b"},
		"tb": {"only_in_b", "only_in_a"},
	} {
		got := stepPayload(steps, node)
		if _, ok := got[want.has]; !ok {
			t.Errorf("node %s lacks %q, a column its own source sent. Payload: %v", node, want.has, got)
		}
		if _, ok := got[want.lacks]; ok {
			t.Errorf("node %s shows %q, a column from the other branch. Payload: %v", node, want.lacks, got)
		}
	}

	// The Test button does not ask for a partial preview, and that workflow
	// still cannot run, so it is still refused.
	if code, _, _ := postSimulation(t, map[string]any{
		"workflow": workflow,
		"message":  map[string]any{"k": "v"},
	}); code == http.StatusOK {
		t.Error("the Test button's request succeeded for a workflow the engine will not start")
	}
}

// The editor highlights the path a simulated message took from two fields on
// each step: `taken_edges`, the edges the node's output travelled along, and
// `skipped`, set on a node nothing reached. The request is shaped the way the
// editor sends it -- a branch edge carries its handle in `source_handle` and a
// copy of it in `config.label` -- so a rename on either side of the wire breaks
// this test rather than the canvas.
func TestSimulationEndpointReportsThePathTheMessageTook(t *testing.T) {
	workflow := map[string]any{
		"name": "gold customers one way, everyone else the other",
		"nodes": []map[string]any{
			{"id": "src", "type": "source", "ref_id": "orders"},
			{"id": "is-gold", "type": "condition", "config": map[string]any{"field": "tier", "operator": "=", "value": "gold"}},
			{"id": "gold", "type": "transformation", "config": map[string]any{"transType": "set", "column.lane": "'gold'"}},
			{"id": "other", "type": "transformation", "config": map[string]any{"transType": "set", "column.lane": "'other'"}},
		},
		"edges": []map[string]any{
			{"id": "e-in", "source_id": "src", "target_id": "is-gold", "config": map[string]any{"label": ""}},
			{"id": "e-true", "source_id": "is-gold", "target_id": "gold", "source_handle": "true", "config": map[string]any{"label": "true"}},
			{"id": "e-false", "source_id": "is-gold", "target_id": "other", "source_handle": "false", "config": map[string]any{"label": "false"}},
		},
	}

	code, steps, body := postSimulation(t, map[string]any{
		"workflow": workflow,
		"messages": map[string]any{"src": map[string]any{"tier": "gold"}},
		"partial":  true,
	})
	if code != http.StatusOK {
		t.Fatalf("simulation returned %d: %s", code, body)
	}

	takenBy := map[string][]any{}
	skipped := map[string]bool{}
	for _, s := range steps {
		id, _ := s["node_id"].(string)
		if edges, ok := s["taken_edges"].([]any); ok {
			takenBy[id] = append(takenBy[id], edges...)
		}
		if v, _ := s["skipped"].(bool); v {
			skipped[id] = true
		}
	}

	for node, want := range map[string]string{"src": "e-in", "is-gold": "e-true"} {
		if got := takenBy[node]; len(got) != 1 || got[0] != want {
			t.Errorf("node %s reports taken_edges %v, want [%s]. Response: %s", node, got, want, body)
		}
	}
	if !skipped["other"] {
		t.Errorf("the node on the branch not taken is not reported skipped. Response: %s", body)
	}
	if skipped["gold"] {
		t.Errorf("the node on the branch taken is reported skipped. Response: %s", body)
	}
}
