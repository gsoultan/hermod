package openai

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/hermod/pkg/llm"
)

type capture struct {
	path   string
	header http.Header
	body   map[string]any
}

func server(t *testing.T, status int, headers map[string]string, reply any) (*httptest.Server, *capture) {
	t.Helper()
	c := &capture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.path = r.URL.Path
		c.header = r.Header.Clone()
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &c.body)
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(reply)
	}))
	t.Cleanup(srv.Close)
	return srv, c
}

func textReply(content, finish string) map[string]any {
	return map[string]any{
		"id":    "chatcmpl-1",
		"model": "m-served",
		"choices": []any{map[string]any{
			"message":       map[string]any{"role": "assistant", "content": content},
			"finish_reason": finish,
		}},
		"usage": map[string]any{"prompt_tokens": 11, "completion_tokens": 7},
	}
}

func TestChat_TextRoundTrip(t *testing.T) {
	srv, c := server(t, 200, nil, textReply("hello", "stop"))
	p := New(Config{BaseURL: srv.URL, APIKey: "k1"})

	resp, err := p.Chat(t.Context(), llm.ChatRequest{
		Model:    "m1",
		System:   "be brief",
		Messages: []llm.Message{{Role: llm.RoleUser, Text: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.path != "/chat/completions" {
		t.Errorf("path = %q", c.path)
	}
	if got := c.header.Get("Authorization"); got != "Bearer k1" {
		t.Errorf("Authorization = %q", got)
	}
	msgs := c.body["messages"].([]any)
	if len(msgs) != 2 || msgs[0].(map[string]any)["role"] != "system" || msgs[1].(map[string]any)["content"] != "hi" {
		t.Errorf("messages = %v", msgs)
	}
	if resp.Text != "hello" || resp.StopReason != llm.StopEnd || resp.Model != "m-served" || resp.Provider != "openai" {
		t.Errorf("resp = %+v", resp)
	}
	if resp.Usage != (llm.Usage{InputTokens: 11, OutputTokens: 7}) {
		t.Errorf("usage = %+v", resp.Usage)
	}
}

func TestChat_RequiresModel(t *testing.T) {
	p := New(Config{BaseURL: "http://unused"})
	if _, err := p.Chat(t.Context(), llm.ChatRequest{Messages: []llm.Message{{Role: llm.RoleUser, Text: "x"}}}); err == nil {
		t.Fatal("expected an error without a model")
	}
}

func TestChat_ToolsRoundTrip(t *testing.T) {
	reply := map[string]any{
		"model": "m1",
		"choices": []any{map[string]any{
			"message": map[string]any{"role": "assistant", "content": nil, "tool_calls": []any{
				map[string]any{"id": "call_1", "type": "function", "function": map[string]any{"name": "lookup", "arguments": `{"id":7}`}},
			}},
			"finish_reason": "tool_calls",
		}},
	}
	srv, c := server(t, 200, nil, reply)
	p := New(Config{BaseURL: srv.URL})

	resp, err := p.Chat(t.Context(), llm.ChatRequest{
		Model: "m1",
		Tools: []llm.ToolSpec{{Name: "lookup", Description: "find", Schema: map[string]any{"type": "object"}}},
		Messages: []llm.Message{
			{Role: llm.RoleUser, Text: "find 7"},
			{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call_0", Name: "lookup", Input: json.RawMessage(`{"id":6}`)}}},
			{Role: llm.RoleUser, ToolResults: []llm.ToolResult{{CallID: "call_0", Name: "lookup", Content: "none"}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	tools := c.body["tools"].([]any)
	fn := tools[0].(map[string]any)["function"].(map[string]any)
	if fn["name"] != "lookup" || fn["description"] != "find" {
		t.Errorf("tools = %v", tools)
	}
	msgs := c.body["messages"].([]any)
	asst := msgs[1].(map[string]any)
	tc := asst["tool_calls"].([]any)[0].(map[string]any)
	if tc["id"] != "call_0" || tc["function"].(map[string]any)["arguments"] != `{"id":6}` {
		t.Errorf("assistant tool call = %v", asst)
	}
	tool := msgs[2].(map[string]any)
	if tool["role"] != "tool" || tool["tool_call_id"] != "call_0" || tool["content"] != "none" {
		t.Errorf("tool result = %v", tool)
	}
	if resp.StopReason != llm.StopToolUse || len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "lookup" || string(resp.ToolCalls[0].Input) != `{"id":7}` {
		t.Errorf("resp = %+v", resp)
	}
}

func TestChat_StructuredOutputModes(t *testing.T) {
	schema := map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{"type": "string"}}}

	srv, c := server(t, 200, nil, textReply(`{"a":"x"}`, "stop"))
	if _, err := New(Config{BaseURL: srv.URL}).Chat(t.Context(), llm.ChatRequest{Model: "m", ResponseSchema: schema, Messages: []llm.Message{{Role: llm.RoleUser, Text: "q"}}}); err != nil {
		t.Fatal(err)
	}
	rf := c.body["response_format"].(map[string]any)
	if rf["type"] != "json_schema" || rf["json_schema"].(map[string]any)["schema"] == nil {
		t.Errorf("native response_format = %v", rf)
	}
	if _, ok := c.body["max_completion_tokens"]; !ok {
		t.Errorf("openai mode should send max_completion_tokens: %v", c.body)
	}

	srv2, c2 := server(t, 200, nil, textReply(`{"a":"x"}`, "stop"))
	if _, err := New(Config{BaseURL: srv2.URL, JSONMode: JSONObject, LegacyMaxTokens: true}).Chat(t.Context(), llm.ChatRequest{Model: "m", ResponseSchema: schema, Messages: []llm.Message{{Role: llm.RoleUser, Text: "q"}}}); err != nil {
		t.Fatal(err)
	}
	rf2 := c2.body["response_format"].(map[string]any)
	if rf2["type"] != "json_object" {
		t.Errorf("json_object response_format = %v", rf2)
	}
	sys := c2.body["messages"].([]any)[0].(map[string]any)
	if sys["role"] != "system" || !strings.Contains(sys["content"].(string), `"properties"`) {
		t.Errorf("json_object mode must put the schema in the system prompt: %v", sys)
	}
	if _, ok := c2.body["max_tokens"]; !ok {
		t.Errorf("legacy mode should send max_tokens: %v", c2.body)
	}
}

func TestChat_RateLimitIsRetryableWithRetryAfter(t *testing.T) {
	srv, _ := server(t, 429, map[string]string{"Retry-After": "3"}, map[string]any{"error": map[string]any{"message": "slow down"}})
	_, err := New(Config{BaseURL: srv.URL}).Chat(t.Context(), llm.ChatRequest{Model: "m", Messages: []llm.Message{{Role: llm.RoleUser, Text: "q"}}})
	var apiErr *llm.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v", err)
	}
	if !apiErr.Retryable || apiErr.RetryAfter != 3*time.Second || apiErr.Status != 429 || !strings.Contains(apiErr.Message, "slow down") {
		t.Errorf("apiErr = %+v", apiErr)
	}
}

func TestChat_BadRequestIsNotRetryable(t *testing.T) {
	srv, _ := server(t, 400, nil, map[string]any{"error": map[string]any{"message": "bad model"}})
	_, err := New(Config{BaseURL: srv.URL}).Chat(t.Context(), llm.ChatRequest{Model: "m", Messages: []llm.Message{{Role: llm.RoleUser, Text: "q"}}})
	if err == nil || llm.IsRetryable(err) {
		t.Fatalf("err = %v, retryable = %v", err, llm.IsRetryable(err))
	}
}

func TestEmbed(t *testing.T) {
	srv, c := server(t, 200, nil, map[string]any{
		"model": "e1",
		"data": []any{
			map[string]any{"index": 1, "embedding": []float64{0.3, 0.4}},
			map[string]any{"index": 0, "embedding": []float64{0.1, 0.2}},
		},
		"usage": map[string]any{"prompt_tokens": 4},
	})
	resp, err := New(Config{BaseURL: srv.URL}).Embed(t.Context(), llm.EmbedRequest{Model: "e1", Inputs: []string{"a", "b"}})
	if err != nil {
		t.Fatal(err)
	}
	if c.path != "/embeddings" {
		t.Errorf("path = %q", c.path)
	}
	if len(resp.Vectors) != 2 || resp.Vectors[0][0] != float32(0.1) || resp.Vectors[1][1] != float32(0.4) {
		t.Errorf("vectors not in input order: %v", resp.Vectors)
	}
	if resp.Usage.InputTokens != 4 {
		t.Errorf("usage = %+v", resp.Usage)
	}
}

func TestPresets(t *testing.T) {
	for _, name := range []string{"openai", "deepseek", "ollama", "mistral", "groq", "openrouter", "together", "xai"} {
		cfg, ok := Preset(name)
		if !ok || cfg.BaseURL == "" {
			t.Errorf("preset %q missing", name)
		}
	}
	if cfg, _ := Preset("deepseek"); cfg.JSONMode != JSONObject {
		t.Errorf("deepseek should use json_object mode")
	}
}
