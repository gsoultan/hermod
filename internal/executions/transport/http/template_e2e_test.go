package http

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/factory"
	"github.com/gsoultan/hermod/internal/storage"
)

// scriptedLLM is an OpenAI-compatible endpoint answering each call with the
// next scripted reply.
func scriptedLLM(t *testing.T, replies ...string) (*httptest.Server, func() int) {
	t.Helper()
	var mu sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		mu.Lock()
		reply := replies[min(calls, len(replies)-1)]
		calls++
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":   "m",
			"choices": []any{map[string]any{"message": map[string]any{"content": reply}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 50, "completion_tokens": 20},
		})
	}))
	t.Cleanup(srv.Close)
	return srv, func() int { mu.Lock(); defer mu.Unlock(); return calls }
}

// The support triage template does what the plan asks of it: a support
// email is classified, its details are extracted, a reply is drafted, and the
// run waits for a person; once they approve, the reply is sent.
func TestSupportTriageTemplateRunsEndToEnd(t *testing.T) {
	llm, calls := scriptedLLM(t,
		`{"label":"support","confidence":0.93,"reason":"a customer asking about a refund"}`,
		`{"customer_name":"Ada","order_id":"A-1001","category":"refund","urgency":"high","summary":"Wants a refund for a damaged order."}`,
		"Hi Ada, sorry your order arrived damaged. A colleague will confirm your refund shortly. The Support Team",
	)

	raw, err := os.ReadFile("../../../../examples/templates/ai_support_triage.json")
	if err != nil {
		t.Fatal(err)
	}
	var tmpl struct {
		Data storage.WorkflowExportBundle `json:"data"`
	}
	if err := json.Unmarshal(raw, &tmpl); err != nil {
		t.Fatal(err)
	}
	wf := tmpl.Data.Workflow
	wf.VHost = "team-a"
	// Point the AI nodes at the scripted model; everything else is as shipped.
	for i, n := range wf.Nodes {
		if _, ok := n.Config["apiKey"]; ok {
			wf.Nodes[i].Config["provider"] = "openai_compatible"
			wf.Nodes[i].Config["baseUrl"] = llm.URL
			wf.Nodes[i].Config["apiKey"] = ""
		}
	}

	e := newEnv(t)
	// What an import of the bundle writes.
	for _, snk := range tmpl.Data.Sinks {
		snk.VHost = "team-a"
		if err := e.store.CreateSink(t.Context(), snk); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.store.CreateWorkflow(t.Context(), wf); err != nil {
		t.Fatal(err)
	}
	mail := &recordingSink{}
	e.reg.SetFactories(nil, func(cfg factory.SinkConfig) (hermod.Sink, error) {
		return mail, nil
	})

	rec := e.do(t, http.MethodPost, "/api/workflows/"+wf.ID+"/run",
		`{"message":{"from":"ada@example.com","subject":"Damaged order","body":"My order A-1001 arrived broken. I want a refund."}}`,
		storage.RoleEditor, "team-a")
	if rec.Code != http.StatusOK {
		t.Fatalf("run: %d %s", rec.Code, rec.Body.String())
	}
	var run struct {
		Status string `json:"status"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &run)
	if run.Status != "waiting" || calls() != 3 || len(mail.received()) != 0 {
		t.Fatalf("status %q after %d model calls, %d emails: want waiting, 3, 0", run.Status, calls(), len(mail.received()))
	}

	apps, _, err := e.store.ListApprovals(t.Context(), storage.ApprovalFilter{WorkflowID: wf.ID, Status: "pending"})
	if err != nil || len(apps) != 1 || apps[0].NodeID != "approve" {
		t.Fatalf("approvals = %+v, %v", apps, err)
	}
	if err := e.reg.ResumeApproval(t.Context(), apps[0], "approved"); err != nil {
		t.Fatal(err)
	}
	sent := mail.received()
	if len(sent) != 1 {
		t.Fatalf("sent %d emails, want 1", len(sent))
	}
	got := sent[0]
	if !strings.HasPrefix(got["draft_reply"].(string), "Hi Ada") || got["category"] != "refund" || got["from"] != "ada@example.com" {
		t.Fatalf("email = %v", got)
	}
}
