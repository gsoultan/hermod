package gemini

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gsoultan/hermod/pkg/llm"
)

type capture struct {
	path   string
	header http.Header
	body   map[string]any
}

func server(t *testing.T, status int, reply any) (*httptest.Server, *capture) {
	t.Helper()
	c := &capture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.path = r.URL.Path
		c.header = r.Header.Clone()
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &c.body)
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(reply)
	}))
	t.Cleanup(srv.Close)
	return srv, c
}

func TestChat_TextRoundTrip(t *testing.T) {
	srv, c := server(t, 200, map[string]any{
		"candidates": []any{map[string]any{
			"content":      map[string]any{"role": "model", "parts": []any{map[string]any{"text": "hel"}, map[string]any{"text": "lo"}}},
			"finishReason": "STOP",
		}},
		"usageMetadata": map[string]any{"promptTokenCount": 5, "candidatesTokenCount": 2},
		"modelVersion":  "g-served",
		"responseId":    "r1",
	})
	p := New(Config{BaseURL: srv.URL, APIKey: "gk"})
	resp, err := p.Chat(t.Context(), llm.ChatRequest{Model: "g1", System: "sys", Messages: []llm.Message{{Role: llm.RoleUser, Text: "hi"}}, MaxTokens: 50})
	if err != nil {
		t.Fatal(err)
	}
	if c.path != "/v1beta/models/g1:generateContent" {
		t.Errorf("path = %q", c.path)
	}
	if c.header.Get("x-goog-api-key") != "gk" {
		t.Errorf("api key header missing")
	}
	si := c.body["systemInstruction"].(map[string]any)["parts"].([]any)[0].(map[string]any)["text"]
	if si != "sys" {
		t.Errorf("systemInstruction = %v", si)
	}
	gc := c.body["generationConfig"].(map[string]any)
	if gc["maxOutputTokens"].(float64) != 50 {
		t.Errorf("generationConfig = %v", gc)
	}
	if resp.Text != "hello" || resp.StopReason != llm.StopEnd || resp.Usage != (llm.Usage{InputTokens: 5, OutputTokens: 2}) || resp.Model != "g-served" || resp.Provider != "gemini" {
		t.Errorf("resp = %+v", resp)
	}
}

func TestChat_ToolsRoundTrip(t *testing.T) {
	srv, c := server(t, 200, map[string]any{
		"candidates": []any{map[string]any{
			"content": map[string]any{"role": "model", "parts": []any{map[string]any{
				"functionCall": map[string]any{"name": "lookup", "args": map[string]any{"id": 7}},
			}}},
			"finishReason": "STOP",
		}},
	})
	resp, err := New(Config{BaseURL: srv.URL}).Chat(t.Context(), llm.ChatRequest{
		Model: "g1",
		Tools: []llm.ToolSpec{{Name: "lookup", Description: "find", Schema: map[string]any{"type": "object"}}},
		Messages: []llm.Message{
			{Role: llm.RoleUser, Text: "find"},
			{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "x", Name: "lookup", Input: json.RawMessage(`{"id":6}`)}}},
			{Role: llm.RoleUser, ToolResults: []llm.ToolResult{{CallID: "x", Name: "lookup", Content: "none"}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	decl := c.body["tools"].([]any)[0].(map[string]any)["functionDeclarations"].([]any)[0].(map[string]any)
	if decl["name"] != "lookup" {
		t.Errorf("functionDeclarations = %v", decl)
	}
	contents := c.body["contents"].([]any)
	model := contents[1].(map[string]any)
	if model["role"] != "model" || model["parts"].([]any)[0].(map[string]any)["functionCall"].(map[string]any)["name"] != "lookup" {
		t.Errorf("model turn = %v", model)
	}
	fr := contents[2].(map[string]any)["parts"].([]any)[0].(map[string]any)["functionResponse"].(map[string]any)
	if fr["name"] != "lookup" || fr["response"].(map[string]any)["content"] != "none" {
		t.Errorf("functionResponse = %v", fr)
	}
	if resp.StopReason != llm.StopToolUse || len(resp.ToolCalls) != 1 || string(resp.ToolCalls[0].Input) != `{"id":7}` || resp.ToolCalls[0].ID == "" {
		t.Errorf("resp = %+v", resp)
	}
}

func TestChat_StructuredOutput(t *testing.T) {
	srv, c := server(t, 200, map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{map[string]any{"text": "{}"}}}, "finishReason": "STOP"}}})
	schema := map[string]any{"type": "object"}
	if _, err := New(Config{BaseURL: srv.URL}).Chat(t.Context(), llm.ChatRequest{Model: "g1", ResponseSchema: schema, Messages: []llm.Message{{Role: llm.RoleUser, Text: "q"}}}); err != nil {
		t.Fatal(err)
	}
	gc := c.body["generationConfig"].(map[string]any)
	if gc["responseMimeType"] != "application/json" || gc["responseJsonSchema"] == nil {
		t.Errorf("generationConfig = %v", gc)
	}
}

func TestChat_SafetyIsRefusalAndErrorsMap(t *testing.T) {
	srv, _ := server(t, 200, map[string]any{"candidates": []any{map[string]any{"finishReason": "SAFETY"}}})
	resp, err := New(Config{BaseURL: srv.URL}).Chat(t.Context(), llm.ChatRequest{Model: "g1", Messages: []llm.Message{{Role: llm.RoleUser, Text: "q"}}})
	if err != nil || resp.StopReason != llm.StopRefusal {
		t.Fatalf("resp = %+v err = %v", resp, err)
	}
	srv2, _ := server(t, 503, map[string]any{"error": map[string]any{"message": "overloaded"}})
	_, err = New(Config{BaseURL: srv2.URL}).Chat(t.Context(), llm.ChatRequest{Model: "g1", Messages: []llm.Message{{Role: llm.RoleUser, Text: "q"}}})
	if !llm.IsRetryable(err) {
		t.Fatalf("503 should be retryable: %v", err)
	}
}

func TestEmbed(t *testing.T) {
	srv, c := server(t, 200, map[string]any{"embeddings": []any{
		map[string]any{"values": []float64{0.1}}, map[string]any{"values": []float64{0.2}},
	}})
	resp, err := New(Config{BaseURL: srv.URL}).Embed(t.Context(), llm.EmbedRequest{Model: "e1", Inputs: []string{"a", "b"}})
	if err != nil {
		t.Fatal(err)
	}
	if c.path != "/v1beta/models/e1:batchEmbedContents" {
		t.Errorf("path = %q", c.path)
	}
	reqs := c.body["requests"].([]any)
	if len(reqs) != 2 || reqs[0].(map[string]any)["model"] != "models/e1" {
		t.Errorf("requests = %v", reqs)
	}
	if len(resp.Vectors) != 2 || resp.Vectors[1][0] != float32(0.2) {
		t.Errorf("vectors = %v", resp.Vectors)
	}
}
