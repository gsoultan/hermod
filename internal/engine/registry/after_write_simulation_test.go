package registry

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// Run Simulation failed the same way the Available Fields refresh did: a
// data_conversion on `after.scheduled_at`, the spelling the editor offers, ahead
// of an api_lookup made every {{.after.x}} in the lookup's body resolve to "",
// and the operator's session API refused the body. This is the strict path -- a
// reachable sink, stored references -- and the node chain it runs is the one a
// running engine runs, so the row reaching the lookup and the sink is asserted
// here, not only through the editor's partial preview.
func TestRunSimulationKeepsTheRowPastANodeThatWritesAnAfterField(t *testing.T) {
	const userID = "07581be3-9ecd-5da5-865e-34ab8aae1fec"

	var mu sync.Mutex
	var sent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		sent = string(raw)
		mu.Unlock()
		// Decoded the way the session API decodes it: "" is not a uuid.
		var req struct {
			CreatedByID uuid.UUID `json:"created_by_id"`
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.Unmarshal(raw, &req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code":"invalid_argument","message":"invalid request body"}`))
			return
		}
		_, _ = w.Write([]byte(`{"token":"tok-1"}`))
	}))
	defer srv.Close()

	reg := newSimRegistry(t)
	wf := storage.Workflow{
		ID: "sessions-wf", Name: "reminder sessions",
		Nodes: []storage.WorkflowNode{
			{ID: "src", Type: "source", RefID: "src-1"},
			{ID: "conv", Type: "transformation", Config: map[string]any{
				"transType": "data_conversion",
				"conversions": []any{map[string]any{
					"field": "after.scheduled_at", "targetType": "date", "outputFormat": "2006-01-02",
				}},
			}},
			{ID: "lookup", Type: "transformation", Config: map[string]any{
				"transType": "api_lookup", "method": "POST", "url": srv.URL + "/v1/sessions",
				"body":        `{"created_by_id": "{{.after.user_id}}"}`,
				"targetField": "session", "ttl": "0",
			}},
			{ID: "snk", Type: "sink", RefID: "snk-1"},
		},
		Edges: []storage.WorkflowEdge{
			{ID: "e1", SourceID: "src", TargetID: "conv"},
			{ID: "e2", SourceID: "conv", TargetID: "lookup"},
			{ID: "e3", SourceID: "lookup", TargetID: "snk"},
		},
	}

	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	message.PopulateFromMap(msg, map[string]any{
		"id": "sample-query-1", "operation": "snapshot",
		"after": map[string]any{"user_id": userID, "scheduled_at": "2026-09-27T22:00:00Z"},
	})

	steps, err := reg.TestWorkflow(t.Context(), wf, msg)
	if err != nil {
		t.Fatalf("Run Simulation refused the workflow: %v", err)
	}
	if s := stepOf(t, steps, "lookup"); s.Error != "" {
		t.Errorf("Run Simulation failed the lookup: %s", s.Error)
	}
	mu.Lock()
	body := sent
	mu.Unlock()
	if !strings.Contains(body, userID) {
		t.Errorf("the lookup sent %s, not the row's user_id", body)
	}

	// The node after the lookup is handed the whole row: the converted field,
	// the column the conversion did not touch, and the lookup's result.
	after, _ := payloadOf(steps, "lookup")["after"].(json.RawMessage)
	var image map[string]any
	if err := json.Unmarshal(after, &image); err != nil {
		t.Fatalf("the lookup's output has no readable after-image (%v): %s", err, after)
	}
	if image["user_id"] != userID || image["session"] == nil || image["scheduled_at"] != "2026-09-27" {
		t.Errorf("the row after the lookup is %v; want user_id kept, scheduled_at converted and session added", image)
	}
}
