package agent

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gsoultan/hermod/internal/engine/registry/interfaces"
	"github.com/gsoultan/hermod/pkg/llm"
)

func TestAgent_FinalAnswerGoesToTargetField(t *testing.T) {
	p := &scriptedProvider{turns: []func(llm.ChatRequest) llm.ChatResponse{say("All done.")}}
	nctx := newFakeNodeContext()
	msg := inputMessage(t, map[string]any{"email": "ada@example.com"})

	out, branch, err := newNode(p).Execute(t.Context(), nctx, "wf", agentNode(map[string]any{"targetField": "answer"}), msg)
	if err != nil {
		t.Fatal(err)
	}
	if branch != "" || len(out) != 1 || out[0].Data()["answer"] != "All done." {
		t.Fatalf("branch=%q out=%v", branch, out)
	}
	req := p.requests()[0]
	if len(req.Tools) != 2 || req.Tools[0].Name != "find_customer" || req.Tools[1].Name != "create_ticket" {
		t.Fatalf("tools offered = %+v", req.Tools)
	}
	if req.Model != "fake-model" {
		t.Fatalf("model = %q", req.Model)
	}
}

func TestAgent_ReadToolRunsTheLookupWithFixedConfig(t *testing.T) {
	p := &scriptedProvider{turns: []func(llm.ChatRequest) llm.ChatResponse{
		call("c1", "find_customer", `{"email":"ada@example.com"}`),
		say("Ada is a customer."),
	}}
	nctx := newFakeNodeContext()
	msg := inputMessage(t, map[string]any{"text": "who am I"})

	out, _, err := newNode(p).Execute(t.Context(), nctx, "wf", agentNode(nil), msg)
	if err != nil {
		t.Fatal(err)
	}
	calls := nctx.lookupCalls()
	if len(calls) != 1 || calls[0].transType != "db_lookup" {
		t.Fatalf("lookups = %+v", calls)
	}
	if calls[0].config["sourceId"] != "crm" || calls[0].config["table"] != "customers" {
		t.Fatalf("lookup config = %+v", calls[0].config)
	}
	if calls[0].data["email"] != "ada@example.com" {
		t.Fatalf("lookup message = %+v", calls[0].data)
	}
	res := lastToolResults(p)
	if len(res) != 1 || res[0].CallID != "c1" || res[0].IsError || !strings.Contains(res[0].Content, "Ada") {
		t.Fatalf("tool results = %+v", res)
	}
	if out[0].Data()[DefaultTargetField] != "Ada is a customer." {
		t.Fatalf("answer = %v", out[0].Data()[DefaultTargetField])
	}
}

func TestAgent_TranscriptIsRecordedOnTheMessage(t *testing.T) {
	p := &scriptedProvider{turns: []func(llm.ChatRequest) llm.ChatResponse{
		call("c1", "find_customer", `{"email":"ada@example.com"}`),
		say("done"),
	}}
	msg := inputMessage(t, nil)
	if _, _, err := newNode(p).Execute(t.Context(), newFakeNodeContext(), "wf", agentNode(nil), msg); err != nil {
		t.Fatal(err)
	}
	tr, ok := msg.Data()[DefaultTranscriptField].(map[string]any)
	if !ok {
		t.Fatalf("transcript = %#v", msg.Data()[DefaultTranscriptField])
	}
	if tr["status"] != "completed" {
		t.Fatalf("status = %v", tr["status"])
	}
	usage, _ := tr["usage"].(map[string]any)
	if usage["input_tokens"] != float64(20) || usage["output_tokens"] != float64(10) {
		t.Fatalf("usage = %v", usage)
	}
	raw, _ := json.Marshal(tr["entries"])
	for _, want := range []string{`"kind":"model"`, `"kind":"tool_call"`, `"kind":"tool_result"`, `"tool":"find_customer"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("transcript entries miss %s: %s", want, raw)
		}
	}
}

func TestAgent_StepLimitFailsClosedWithPartialTranscript(t *testing.T) {
	loop := call("c", "find_customer", `{"email":"a@b.c"}`)
	p := &scriptedProvider{turns: []func(llm.ChatRequest) llm.ChatResponse{loop, loop, loop, loop}}
	msg := inputMessage(t, nil)

	out, _, err := newNode(p).Execute(t.Context(), newFakeNodeContext(), "wf", agentNode(map[string]any{"maxSteps": float64(2)}), msg)
	if err == nil || !strings.Contains(err.Error(), "step") {
		t.Fatalf("err = %v", err)
	}
	if len(p.requests()) != 2 {
		t.Fatalf("model was called %d times, want 2", len(p.requests()))
	}
	if len(out) != 1 {
		t.Fatalf("the failing message must stay on the error path, got %v", out)
	}
	tr, _ := msg.Data()[DefaultTranscriptField].(map[string]any)
	if tr["status"] != "failed" || !strings.Contains(tr["error"].(string), "step") {
		t.Fatalf("transcript = %v", tr)
	}
}

func TestAgent_MaxStepsHasAHardCap(t *testing.T) {
	cfg, err := parseConfig(agentNode(map[string]any{"maxSteps": "100000"}).Config)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.maxSteps != hardMaxSteps {
		t.Fatalf("maxSteps = %d, want the hard cap %d", cfg.maxSteps, hardMaxSteps)
	}
	cfg, _ = parseConfig(agentNode(nil).Config)
	if cfg.maxSteps != defaultMaxSteps {
		t.Fatalf("default maxSteps = %d", cfg.maxSteps)
	}
}

func TestAgent_TokenBudgetFailsClosed(t *testing.T) {
	big := func(llm.ChatRequest) llm.ChatResponse {
		return llm.ChatResponse{
			ToolCalls:  []llm.ToolCall{{ID: "c", Name: "find_customer", Input: json.RawMessage(`{"email":"a@b.c"}`)}},
			StopReason: llm.StopToolUse,
			Usage:      llm.Usage{InputTokens: 900, OutputTokens: 200},
		}
	}
	p := &scriptedProvider{turns: []func(llm.ChatRequest) llm.ChatResponse{big, big, big}}
	msg := inputMessage(t, nil)

	_, _, err := newNode(p).Execute(t.Context(), newFakeNodeContext(), "wf", agentNode(map[string]any{"maxTotalTokens": float64(1000)}), msg)
	if !errors.Is(err, llm.ErrBudgetExceeded) {
		t.Fatalf("err = %v, want the budget error", err)
	}
	if len(p.requests()) != 1 {
		t.Fatalf("model was called %d times after the budget was spent", len(p.requests()))
	}
}

func TestAgent_ConfigErrors(t *testing.T) {
	cases := map[string]map[string]any{
		"no goal":           {"goal": ""},
		"no tools":          {"tools": []any{}},
		"unknown tool kind": {"tools": []any{map[string]any{"name": "x", "kind": "exec_shell"}}},
		"bad tool name":     {"tools": []any{map[string]any{"name": "has space", "kind": "db_lookup"}}},
		"duplicate tool": {"tools": []any{
			map[string]any{"name": "x", "kind": "db_lookup"}, map[string]any{"name": "x", "kind": "api_lookup"},
		}},
		"sink without node": {"tools": []any{map[string]any{"name": "x", "kind": "sink"}}},
	}
	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			p := &scriptedProvider{}
			_, _, err := newNode(p).Execute(t.Context(), newFakeNodeContext(), "wf", agentNode(extra), inputMessage(t, nil))
			if err == nil {
				t.Fatal("want a config error")
			}
			if len(p.requests()) != 0 {
				t.Fatal("the model must not be called with a broken config")
			}
		})
	}
}

func TestAgent_ToolsMayBeGivenAsJSONText(t *testing.T) {
	cfg, err := parseConfig(map[string]any{
		"goal":  "g",
		"tools": `[{"name":"t","kind":"api_lookup","parameters":[{"name":"q","type":"string"}]}]`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.tools) != 1 || cfg.tools[0].Name != "t" || cfg.tools[0].Params[0].Name != "q" {
		t.Fatalf("tools = %+v", cfg.tools)
	}
}

// The executor is reachable the way a workflow reaches it: registered under
// ai_agent, building its provider from the node's connection settings.
func TestAgent_RegisteredExecutorTalksToAConfiguredProvider(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if calls.Add(1) == 1 {
			if tools, _ := body["tools"].([]any); len(tools) != 2 {
				t.Errorf("tools sent = %v", body["tools"])
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{
				"message": map[string]any{"tool_calls": []any{map[string]any{
					"id": "call_1", "type": "function",
					"function": map[string]any{"name": "find_customer", "arguments": `{"email":"ada@example.com"}`},
				}}},
				"finish_reason": "tool_calls",
			}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{
			"message": map[string]any{"content": "Found Ada."}, "finish_reason": "stop",
		}}})
	}))
	t.Cleanup(srv.Close)

	exec, ok := interfaces.GetNodeExecutor("ai_agent")
	if !ok {
		t.Fatal("ai_agent is not registered")
	}
	node := agentNode(map[string]any{"provider": "openai_compatible", "baseUrl": srv.URL, "model": "m"})
	nctx := newFakeNodeContext()
	msg := inputMessage(t, nil)
	out, _, err := exec.Execute(t.Context(), nctx, "wf", node, msg)
	if err != nil {
		t.Fatal(err)
	}
	if out[0].Data()[DefaultTargetField] != "Found Ada." || len(nctx.lookupCalls()) != 1 {
		t.Fatalf("answer = %v lookups = %d", out[0].Data()[DefaultTargetField], len(nctx.lookupCalls()))
	}
}
