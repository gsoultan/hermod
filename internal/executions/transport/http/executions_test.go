package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
	_ "github.com/gsoultan/hermod/pkg/comm/transformer/genai"
)

// A run through an AI node shows that node's token usage, and the run's
// total, in the run history: usage travels on the message into the trace.
func TestExecutionsShowAITokenUsage(t *testing.T) {
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"model":"m","choices":[{"message":{"content":"Thanks, refund on its way."},"finish_reason":"stop"}],"usage":{"prompt_tokens":120,"completion_tokens":30}}`))
	}))
	defer llm.Close()

	e := newEnv(t)
	if err := e.store.CreateWorkflow(t.Context(), storage.Workflow{
		ID: "wf-ai", Name: "wf-ai", VHost: "team-a",
		Nodes: []storage.WorkflowNode{
			{ID: "src", Type: "source", RefID: "src-1"},
			{ID: "draft", Type: "transformation", Config: map[string]any{
				"transType": "ai_prompt", "provider": "openai_compatible", "baseUrl": llm.URL, "model": "m",
				"prompt": "Reply to: {{.body}}", "targetField": "reply",
			}},
			{ID: "out", Type: "sink", RefID: "snk-1"},
		},
		Edges: []storage.WorkflowEdge{{ID: "e1", SourceID: "src", TargetID: "draft"}, {ID: "e2", SourceID: "draft", TargetID: "out"}},
	}); err != nil {
		t.Fatal(err)
	}
	if rec := e.do(t, http.MethodPost, "/api/workflows/wf-ai/run", `{"message":{"body":"refund please"}}`, storage.RoleEditor, "team-a"); rec.Code != http.StatusOK {
		t.Fatalf("run: %d %s", rec.Code, rec.Body.String())
	}

	rec := e.do(t, http.MethodGet, "/api/workflows/wf-ai/executions", "", storage.RoleViewer, "team-a")
	var resp struct {
		Executions []struct {
			AI struct {
				Calls        int64 `json:"calls"`
				InputTokens  int64 `json:"input_tokens"`
				OutputTokens int64 `json:"output_tokens"`
			} `json:"ai"`
			Steps []struct {
				NodeID string `json:"node_id"`
				AI     struct {
					InputTokens int64 `json:"input_tokens"`
				} `json:"ai"`
			} `json:"steps"`
		} `json:"executions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || len(resp.Executions) != 1 {
		t.Fatalf("list: %v %s", err, rec.Body.String())
	}
	ex := resp.Executions[0]
	if ex.AI.Calls != 1 || ex.AI.InputTokens != 120 || ex.AI.OutputTokens != 30 {
		t.Fatalf("run AI usage = %+v", ex.AI)
	}
	for _, s := range ex.Steps {
		if s.NodeID == "draft" && s.AI.InputTokens != 120 {
			t.Fatalf("draft step usage = %+v", s.AI)
		}
	}
}

type listBody struct {
	Executions []struct {
		RunID      string `json:"run_id"`
		Status     string `json:"status"`
		DurationMs int64  `json:"duration_ms"`
		StepCount  int    `json:"step_count"`
		AI         struct {
			Calls int64 `json:"calls"`
		} `json:"ai"`
		Steps []struct {
			NodeID   string         `json:"node_id"`
			NodeType string         `json:"node_type"`
			Output   map[string]any `json:"output"`
		} `json:"steps"`
	} `json:"executions"`
	NextBefore string `json:"next_before"`
}

func (e *env) manualRun(t *testing.T, body string) string {
	t.Helper()
	rec := e.do(t, http.MethodPost, "/api/workflows/wf-1/run", body, storage.RoleEditor, "team-a")
	if rec.Code != http.StatusOK {
		t.Fatalf("run: status %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		RunID string `json:"run_id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	return resp.RunID
}

func TestListExecutions(t *testing.T) {
	e := newEnv(t)
	first := e.manualRun(t, `{"message":{"n":1}}`)
	second := e.manualRun(t, `{"message":{"n":2}}`)

	rec := e.do(t, http.MethodGet, "/api/workflows/wf-1/executions", "", storage.RoleViewer, "team-a")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp listBody
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Executions) != 2 {
		t.Fatalf("executions = %+v", resp.Executions)
	}
	ids := map[string]bool{resp.Executions[0].RunID: true, resp.Executions[1].RunID: true}
	if !ids[first] || !ids[second] {
		t.Fatalf("listed %v, want runs %s and %s", ids, first, second)
	}
	ex := resp.Executions[0]
	if ex.Status != "succeeded" || ex.StepCount != 3 || len(ex.Steps) != 3 || ex.Steps[1].NodeType != "set" {
		t.Fatalf("execution = %+v", ex)
	}
	if ex.Steps[0].Output != nil {
		t.Fatal("the list carries step outputs; only a single run should")
	}

	// limit=1 pages with a cursor.
	rec = e.do(t, http.MethodGet, "/api/workflows/wf-1/executions?limit=1", "", storage.RoleViewer, "team-a")
	var page listBody
	_ = json.Unmarshal(rec.Body.Bytes(), &page)
	if len(page.Executions) != 1 || page.NextBefore == "" {
		t.Fatalf("page = %+v", page)
	}
}

func TestGetExecutionIncludesOutputs(t *testing.T) {
	e := newEnv(t)
	run := e.manualRun(t, `{"message":{"name":"Ada"}}`)

	rec := e.do(t, http.MethodGet, "/api/workflows/wf-1/executions/"+run, "", storage.RoleViewer, "team-a")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var ex struct {
		RunID string `json:"run_id"`
		Steps []struct {
			NodeID string         `json:"node_id"`
			Output map[string]any `json:"output"`
		} `json:"steps"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &ex)
	if ex.RunID != run || len(ex.Steps) != 3 || ex.Steps[1].Output["greeting"] != "hello" {
		t.Fatalf("execution = %+v", ex)
	}

	if rec := e.do(t, http.MethodGet, "/api/workflows/wf-1/executions/no-such-run", "", storage.RoleViewer, "team-a"); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown run: status %d", rec.Code)
	}
}

func TestReplayExecutionRunsTheSameInputAgain(t *testing.T) {
	e := newEnv(t)
	run := e.manualRun(t, `{"message":{"name":"Ada"}}`)

	rec := e.do(t, http.MethodPost, "/api/workflows/wf-1/executions/"+run+"/replay", "", storage.RoleEditor, "team-a")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		RunID    string `json:"run_id"`
		ReplayOf string `json:"replay_of"`
		Status   string `json:"status"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.RunID == "" || resp.RunID == run || resp.ReplayOf != run || resp.Status != "completed" {
		t.Fatalf("replay = %+v", resp)
	}
	got := e.sink.received()
	if len(got) != 2 || got[1]["name"] != "Ada" {
		t.Fatalf("sink received %v, want the original input twice", got)
	}
}

func TestExecutionsAccess(t *testing.T) {
	e := newEnv(t)
	run := e.manualRun(t, `{"message":{"a":1}}`)
	cases := []struct {
		name, method, path string
		role               storage.Role
		vhosts             []string
		want               int
	}{
		{"list, other vhost", http.MethodGet, "/api/workflows/wf-1/executions", storage.RoleViewer, []string{"team-b"}, http.StatusForbidden},
		{"get, other vhost", http.MethodGet, "/api/workflows/wf-1/executions/" + run, storage.RoleEditor, []string{"team-b"}, http.StatusForbidden},
		{"replay, viewer", http.MethodPost, "/api/workflows/wf-1/executions/" + run + "/replay", storage.RoleViewer, []string{"team-a"}, http.StatusForbidden},
		{"replay, other vhost", http.MethodPost, "/api/workflows/wf-1/executions/" + run + "/replay", storage.RoleEditor, []string{"team-b"}, http.StatusForbidden},
		{"list, unknown workflow", http.MethodGet, "/api/workflows/nope/executions", storage.RoleAdministrator, nil, http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if rec := e.do(t, tc.method, tc.path, "", tc.role, tc.vhosts...); rec.Code != tc.want {
				t.Fatalf("status %d, want %d: %s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
	if n := len(e.sink.received()); n != 1 {
		t.Fatalf("sink received %d messages; a refused replay must not run", n)
	}
}
