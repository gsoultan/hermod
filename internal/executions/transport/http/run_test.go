package http

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
)

func TestRunWorkflowWithInput(t *testing.T) {
	e := newEnv(t)
	rec := e.do(t, http.MethodPost, "/api/workflows/wf-1/run", `{"message":{"name":"Ada"}}`, storage.RoleEditor, "team-a")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		RunID      string `json:"run_id"`
		WorkflowID string `json:"workflow_id"`
		Status     string `json:"status"`
		Steps      []struct {
			NodeID string `json:"node_id"`
			Error  string `json:"error"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.RunID == "" || resp.WorkflowID != "wf-1" || resp.Status != "completed" || len(resp.Steps) != 3 {
		t.Fatalf("response = %+v", resp)
	}
	got := e.sink.received()
	if len(got) != 1 || got[0]["name"] != "Ada" || got[0]["greeting"] != "hello" {
		t.Fatalf("sink received %v", got)
	}
	// The run is in the workflow's traces under its id.
	trace, err := e.store.GetMessageTrace(t.Context(), "wf-1", resp.RunID)
	if err != nil || len(trace.Steps) != 3 {
		t.Fatalf("trace = %+v, err %v", trace, err)
	}
}

func TestRunWorkflowAccess(t *testing.T) {
	cases := []struct {
		name   string
		role   storage.Role
		vhosts []string
		want   int
	}{
		{"viewer", storage.RoleViewer, []string{"team-a"}, http.StatusForbidden},
		{"editor of another vhost", storage.RoleEditor, []string{"team-b"}, http.StatusForbidden},
		{"editor without vhosts", storage.RoleEditor, nil, http.StatusForbidden},
		{"admin", storage.RoleAdministrator, nil, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			rec := e.do(t, http.MethodPost, "/api/workflows/wf-1/run", `{"message":{"a":1}}`, tc.role, tc.vhosts...)
			if rec.Code != tc.want {
				t.Fatalf("status %d, want %d: %s", rec.Code, tc.want, rec.Body.String())
			}
			if tc.want != http.StatusOK && len(e.sink.received()) != 0 {
				t.Fatal("a refused run still wrote to the sink")
			}
		})
	}
}

func TestRunWorkflowRejectsBadRequests(t *testing.T) {
	e := newEnv(t)
	cases := []struct {
		name, path, body string
		want             int
	}{
		{"unknown workflow", "/api/workflows/nope/run", `{"message":{}}`, http.StatusNotFound},
		{"not json", "/api/workflows/wf-1/run", `{`, http.StatusBadRequest},
		{"no message", "/api/workflows/wf-1/run", `{}`, http.StatusBadRequest},
		{"not a source node", "/api/workflows/wf-1/run", `{"message":{},"source_node_id":"greet"}`, http.StatusBadRequest},
		{"too large", "/api/workflows/wf-1/run", `{"message":{"x":"` + strings.Repeat("a", 5<<20) + `"}}`, http.StatusRequestEntityTooLarge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := e.do(t, http.MethodPost, tc.path, tc.body, storage.RoleAdministrator)
			if rec.Code != tc.want {
				t.Fatalf("status %d, want %d: %s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
	if len(e.sink.received()) != 0 {
		t.Fatal("a rejected request wrote to the sink")
	}
}
