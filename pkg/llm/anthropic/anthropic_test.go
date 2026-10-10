package anthropic

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gsoultan/hermod/pkg/llm"
)

type capture struct {
	path   string
	header http.Header
	body   map[string]any
	calls  int
}

func server(t *testing.T, status int, headers map[string]string, reply any) (*httptest.Server, *capture) {
	t.Helper()
	c := &capture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.calls++
		c.path = r.URL.Path
		c.header = r.Header.Clone()
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &c.body)
		w.Header().Set("Content-Type", "application/json")
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(reply)
	}))
	t.Cleanup(srv.Close)
	return srv, c
}

func message(content []any, stop string) map[string]any {
	return map[string]any{
		"id": "msg_1", "type": "message", "role": "assistant", "model": "claude-served",
		"content": content, "stop_reason": stop,
		"usage": map[string]any{"input_tokens": 9, "output_tokens": 4},
	}
}

func TestChat_TextRoundTripAndDefaults(t *testing.T) {
	srv, c := server(t, 200, nil, message([]any{map[string]any{"type": "text", "text": "hello"}}, "end_turn"))
	p := New(Config{BaseURL: srv.URL, APIKey: "ak"})
	temp := 0.2
	resp, err := p.Chat(t.Context(), llm.ChatRequest{
		System:      "sys",
		Messages:    []llm.Message{{Role: llm.RoleUser, Text: "hi"}},
		Temperature: &temp,
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.path != "/v1/messages" || c.header.Get("X-Api-Key") != "ak" {
		t.Errorf("path = %q key = %q", c.path, c.header.Get("X-Api-Key"))
	}
	if c.body["model"] != DefaultModel {
		t.Errorf("model = %v, want default %q", c.body["model"], DefaultModel)
	}
	if c.body["max_tokens"].(float64) != DefaultMaxTokens {
		t.Errorf("max_tokens = %v", c.body["max_tokens"])
	}
	if _, sent := c.body["temperature"]; sent {
		t.Errorf("temperature must not be sent: current Claude models reject sampling parameters")
	}
	if resp.Text != "hello" || resp.StopReason != llm.StopEnd || resp.Usage != (llm.Usage{InputTokens: 9, OutputTokens: 4}) || resp.Model != "claude-served" || resp.Provider != "anthropic" || resp.RequestID != "msg_1" {
		t.Errorf("resp = %+v", resp)
	}
}

func TestChat_ToolsRoundTrip(t *testing.T) {
	srv, c := server(t, 200, nil, message([]any{
		map[string]any{"type": "text", "text": "looking"},
		map[string]any{"type": "tool_use", "id": "toolu_2", "name": "lookup", "input": map[string]any{"id": 7}},
	}, "tool_use"))
	resp, err := New(Config{BaseURL: srv.URL, APIKey: "k"}).Chat(t.Context(), llm.ChatRequest{
		Model: "claude-x",
		Tools: []llm.ToolSpec{{Name: "lookup", Description: "find", Schema: map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "integer"}}}}},
		Messages: []llm.Message{
			{Role: llm.RoleUser, Text: "find"},
			{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "toolu_1", Name: "lookup", Input: json.RawMessage(`{"id":6}`)}}},
			{Role: llm.RoleUser, ToolResults: []llm.ToolResult{{CallID: "toolu_1", Name: "lookup", Content: "none", IsError: true}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	tool := c.body["tools"].([]any)[0].(map[string]any)
	if tool["name"] != "lookup" || tool["input_schema"] == nil {
		t.Errorf("tool = %v", tool)
	}
	msgs := c.body["messages"].([]any)
	use := msgs[1].(map[string]any)["content"].([]any)[0].(map[string]any)
	if use["type"] != "tool_use" || use["id"] != "toolu_1" {
		t.Errorf("tool_use = %v", use)
	}
	res := msgs[2].(map[string]any)["content"].([]any)[0].(map[string]any)
	if res["type"] != "tool_result" || res["tool_use_id"] != "toolu_1" || res["is_error"] != true {
		t.Errorf("tool_result = %v", res)
	}
	if resp.StopReason != llm.StopToolUse || resp.Text != "looking" || len(resp.ToolCalls) != 1 || string(resp.ToolCalls[0].Input) != `{"id":7}` {
		t.Errorf("resp = %+v", resp)
	}
}

func TestChat_StructuredOutputUsesOutputConfig(t *testing.T) {
	srv, c := server(t, 200, nil, message([]any{map[string]any{"type": "text", "text": "{}"}}, "end_turn"))
	schema := map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}
	if _, err := New(Config{BaseURL: srv.URL, APIKey: "k"}).Chat(t.Context(), llm.ChatRequest{Model: "m", ResponseSchema: schema, Messages: []llm.Message{{Role: llm.RoleUser, Text: "q"}}}); err != nil {
		t.Fatal(err)
	}
	format := c.body["output_config"].(map[string]any)["format"].(map[string]any)
	if format["type"] != "json_schema" || format["schema"] == nil {
		t.Errorf("output_config.format = %v", format)
	}
}

func TestChat_RefusalAndErrors(t *testing.T) {
	srv, _ := server(t, 200, nil, message([]any{}, "refusal"))
	resp, err := New(Config{BaseURL: srv.URL, APIKey: "k"}).Chat(t.Context(), llm.ChatRequest{Messages: []llm.Message{{Role: llm.RoleUser, Text: "q"}}})
	if err != nil || resp.StopReason != llm.StopRefusal {
		t.Fatalf("resp = %+v err = %v", resp, err)
	}

	srv2, c2 := server(t, 429, map[string]string{"Retry-After": "2"}, map[string]any{"type": "error", "error": map[string]any{"type": "rate_limit_error", "message": "slow"}})
	_, err = New(Config{BaseURL: srv2.URL, APIKey: "k"}).Chat(t.Context(), llm.ChatRequest{Messages: []llm.Message{{Role: llm.RoleUser, Text: "q"}}})
	var apiErr *llm.APIError
	if !errors.As(err, &apiErr) || !apiErr.Retryable || apiErr.Status != 429 || apiErr.RetryAfter != 2*time.Second {
		t.Fatalf("err = %#v", err)
	}
	if c2.calls != 1 {
		t.Errorf("the adapter must not retry on its own (calls = %d); retries belong to llm.WithRetry", c2.calls)
	}

	srv3, _ := server(t, 400, nil, map[string]any{"type": "error", "error": map[string]any{"type": "invalid_request_error", "message": "bad"}})
	_, err = New(Config{BaseURL: srv3.URL, APIKey: "k"}).Chat(t.Context(), llm.ChatRequest{Messages: []llm.Message{{Role: llm.RoleUser, Text: "q"}}})
	if err == nil || llm.IsRetryable(err) {
		t.Fatalf("400 should not be retryable: %v", err)
	}
}

func TestEmbedIsUnsupported(t *testing.T) {
	var p llm.Provider = New(Config{})
	if _, ok := p.(llm.Embedder); ok {
		t.Fatal("Anthropic has no embeddings endpoint; the adapter must not claim to be an Embedder")
	}
}

// Hermod serves many vhosts from one process, so a connection with no key
// must fail rather than fall back to whatever Anthropic credentials the
// server's environment happens to hold.
func TestChat_NeverUsesAmbientCredentials(t *testing.T) {
	srv, c := server(t, 200, nil, message([]any{map[string]any{"type": "text", "text": "x"}}, "end_turn"))
	t.Setenv("ANTHROPIC_API_KEY", "ambient")
	_, err := New(Config{BaseURL: srv.URL}).Chat(t.Context(), llm.ChatRequest{Messages: []llm.Message{{Role: llm.RoleUser, Text: "q"}}})
	if err == nil {
		t.Fatal("expected an error for a connection without an API key")
	}
	if c.calls != 0 {
		t.Fatalf("a request was sent (with key %q)", c.header.Get("X-Api-Key"))
	}
}
