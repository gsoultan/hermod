package registry

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"

	_ "github.com/gsoultan/hermod/pkg/comm/transformer/lookup"
)

// TestChainedLookupSeesTheObjectTheFirstLookupWrote pins that a lookup's
// output reaches the node after it when the workflow runs.
//
// The editor once showed a second lookup an input without the object the first
// one had just written (the per-node preview's result was never handed
// downstream). That was the editor's data, not the engine's: here the first
// lookup writes a whole row as an object and the second keys its request off a
// field inside it, through the same node runner a live workflow uses.
func TestChainedLookupSeesTheObjectTheFirstLookupWrote(t *testing.T) {
	reg := newSimRegistry(t)

	var secondPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/org":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"OrganizationCode": "IMPC",
				"OrganizationName": "PT. IMPACK PRATAMA",
			})
		default:
			secondPath = r.URL.Path
			_ = json.NewEncoder(w).Encode(map[string]any{"tier": "gold"})
		}
	}))
	t.Cleanup(srv.Close)

	wf := storage.Workflow{
		ID: "chained-lookups", Name: "chained lookups",
		Nodes: []storage.WorkflowNode{
			{ID: "src", Type: "source", RefID: "src-1"},
			{ID: "l1", Type: "transformation", Config: map[string]any{
				"transType": "api_lookup", "method": "GET",
				"url": srv.URL + "/org", "targetField": "osiris",
			}},
			{ID: "l2", Type: "transformation", Config: map[string]any{
				"transType": "api_lookup", "method": "GET",
				"url":          srv.URL + "/tier/{{osiris.OrganizationCode}}",
				"responsePath": "tier", "targetField": "tier",
			}},
			{ID: "snk", Type: "sink", RefID: "snk-1"},
		},
		Edges: []storage.WorkflowEdge{
			{ID: "e1", SourceID: "src", TargetID: "l1"},
			{ID: "e2", SourceID: "l1", TargetID: "l2"},
			{ID: "e3", SourceID: "l2", TargetID: "snk"},
		},
	}

	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetAfter([]byte(`{"organization_id":"019a4630"}`))

	steps, err := reg.TestWorkflow(t.Context(), wf, msg)
	if err != nil {
		t.Fatalf("TestWorkflow: %v", err)
	}

	var l2 *WorkflowStepResult
	for i := range steps {
		if steps[i].NodeID == "l2" && steps[i].Payload != nil {
			l2 = &steps[i]
		}
	}
	if l2 == nil {
		t.Fatalf("the second lookup produced no output; steps: %+v", steps)
	}
	if l2.Error != "" {
		t.Fatalf("the second lookup failed: %s", l2.Error)
	}
	if secondPath != "/tier/IMPC" {
		t.Errorf("the second lookup requested %q, want /tier/IMPC: it did not see "+
			"osiris.OrganizationCode, which the first lookup wrote", secondPath)
	}
	osiris, ok := l2.Payload["osiris"].(map[string]any)
	if !ok || osiris["OrganizationCode"] != "IMPC" {
		t.Errorf("osiris was dropped between the two lookups; payload: %v", l2.Payload)
	}
	if l2.Payload["tier"] != "gold" {
		t.Errorf("tier = %v, want gold", l2.Payload["tier"])
	}
}
