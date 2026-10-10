package connect

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gsoultan/hermod/pkg/llm"
)

func TestBuild_Kinds(t *testing.T) {
	cases := map[string]string{
		"anthropic": "anthropic", "claude": "anthropic",
		"openai": "openai", "chatgpt": "openai",
		"gemini": "gemini", "google": "gemini",
		"deepseek": "deepseek", "ollama": "ollama", "mistral": "mistral",
		"groq": "groq", "openrouter": "openrouter", "together": "together", "xai": "xai",
	}
	for kind, want := range cases {
		p, err := Build(Spec{Kind: kind, APIKey: "k"})
		if err != nil {
			t.Errorf("%s: %v", kind, err)
			continue
		}
		if p.Name() != want {
			t.Errorf("%s: Name() = %q, want %q", kind, p.Name(), want)
		}
	}
}

func TestBuild_Rejects(t *testing.T) {
	if _, err := Build(Spec{Kind: "nope"}); err == nil {
		t.Error("unknown kind accepted")
	}
	if _, err := Build(Spec{Kind: "openai_compatible"}); err == nil {
		t.Error("openai_compatible without a base URL accepted")
	}
	if _, err := Build(Spec{Kind: "openai"}); err == nil {
		t.Error("a hosted provider without an API key accepted")
	}
	if _, err := Build(Spec{Kind: "ollama"}); err != nil {
		t.Errorf("ollama needs no key: %v", err)
	}
}

func TestBuild_OpenAICompatibleUsesBaseURLAndRetries(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			t.Errorf("path = %q", r.URL.Path)
		}
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": "ok"}, "finish_reason": "stop"}}})
	}))
	defer srv.Close()

	p, err := Build(Spec{Kind: "openai_compatible", Name: "vllm", BaseURL: srv.URL, Retry: &llm.RetryPolicy{MaxAttempts: 2}})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := p.Chat(t.Context(), llm.ChatRequest{Model: "m", Messages: []llm.Message{{Role: llm.RoleUser, Text: "q"}}})
	if err != nil || resp.Text != "ok" || calls.Load() != 2 {
		t.Fatalf("resp=%+v err=%v calls=%d", resp, err, calls.Load())
	}
	if p.Name() != "vllm" {
		t.Errorf("Name() = %q", p.Name())
	}
}

func TestBuild_FallbackChain(t *testing.T) {
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) }))
	defer down.Close()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": "from-fallback"}, "finish_reason": "stop"}}})
	}))
	defer up.Close()

	p, err := Build(Spec{
		Kind: "openai_compatible", BaseURL: down.URL, Retry: &llm.RetryPolicy{MaxAttempts: 1},
		Fallbacks: []Spec{{Kind: "openai_compatible", BaseURL: up.URL, Model: "fb-model"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := p.Chat(t.Context(), llm.ChatRequest{Model: "primary-model", Messages: []llm.Message{{Role: llm.RoleUser, Text: "q"}}})
	if err != nil || resp.Text != "from-fallback" {
		t.Fatalf("resp=%+v err=%v", resp, err)
	}
}
