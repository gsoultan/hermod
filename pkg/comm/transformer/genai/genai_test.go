package genai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/comm/reply"
	"github.com/gsoultan/hermod/pkg/comm/transformer"
	"github.com/gsoultan/hermod/pkg/infra/state"
	"github.com/gsoultan/hermod/pkg/llm"
)

// fakeLLM is an OpenAI-compatible endpoint that answers each call with the
// next reply and records what it was sent.
type fakeLLM struct {
	t       *testing.T
	mu      sync.Mutex
	replies []string
	bodies  []map[string]any
	headers []http.Header
	srv     *httptest.Server
}

func newFakeLLM(t *testing.T, replies ...string) *fakeLLM {
	t.Helper()
	f := &fakeLLM{t: t, replies: replies}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(b, &body)
		f.mu.Lock()
		f.bodies = append(f.bodies, body)
		f.headers = append(f.headers, r.Header.Clone())
		reply := f.replies[0]
		if len(f.replies) > 1 {
			f.replies = f.replies[1:]
		}
		f.mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "/embeddings") {
			_, _ = w.Write([]byte(reply))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":   "served-model",
			"choices": []any{map[string]any{"message": map[string]any{"content": reply}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 10, "completion_tokens": 5},
		})
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeLLM) config(extra map[string]any) map[string]any {
	cfg := map[string]any{"provider": "openai_compatible", "baseUrl": f.srv.URL, "model": "m1"}
	for k, v := range extra {
		cfg[k] = v
	}
	return cfg
}

func (f *fakeLLM) lastUserText() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	msgs := f.bodies[len(f.bodies)-1]["messages"].([]any)
	return msgs[len(msgs)-1].(map[string]any)["content"].(string)
}

func (f *fakeLLM) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.bodies)
}

func run(t *testing.T, name string, cfg map[string]any, data map[string]any) (map[string]any, error) {
	t.Helper()
	tf, ok := transformer.Get(name)
	if !ok {
		t.Fatalf("%s is not registered", name)
	}
	msg := message.AcquireMessage()
	for k, v := range data {
		msg.SetData(k, v)
	}
	out, err := tf.Transform(t.Context(), msg, cfg)
	if err != nil {
		return nil, err
	}
	return out.Data(), nil
}

func TestPrompt_RendersTemplateAndWritesText(t *testing.T) {
	f := newFakeLLM(t, "Positive")
	data, err := run(t, "ai_prompt", f.config(map[string]any{
		"system":      "You classify sentiment.",
		"prompt":      "Review: {{.review}}",
		"targetField": "sentiment",
	}), map[string]any{"review": "I love it", "card_number": "4111111111111111"})
	if err != nil {
		t.Fatal(err)
	}
	if data["sentiment"] != "Positive" {
		t.Fatalf("sentiment = %v", data["sentiment"])
	}
	got := f.lastUserText()
	if !strings.Contains(got, "Review: I love it") {
		t.Errorf("prompt not rendered: %q", got)
	}
	if strings.Contains(got, "4111") {
		t.Errorf("fields the prompt does not use must not be sent by default: %q", got)
	}
}

func TestPrompt_IncludeDataHonoursAllowListAndMasking(t *testing.T) {
	f := newFakeLLM(t, "ok")
	_, err := run(t, "ai_prompt", f.config(map[string]any{
		"prompt":      "Summarise.",
		"includeData": true,
		"inputFields": "name,email,notes",
		"maskFields":  "email",
		"maskPII":     true,
	}), map[string]any{"name": "Ada", "email": "ada@example.com", "notes": "call 555-123-4567", "salary": 99999})
	if err != nil {
		t.Fatal(err)
	}
	got := f.lastUserText()
	if !strings.Contains(got, `"name":"Ada"`) {
		t.Errorf("allowed field missing: %q", got)
	}
	for _, leaked := range []string{"99999", "ada@example.com"} {
		if strings.Contains(got, leaked) {
			t.Errorf("%q leaked into the prompt: %q", leaked, got)
		}
	}
}

func TestPrompt_JSONOutputMergesOrNests(t *testing.T) {
	f := newFakeLLM(t, `{"topic":"billing","urgent":true}`)
	data, err := run(t, "ai_prompt", f.config(map[string]any{"prompt": "x", "outputMode": "json"}), map[string]any{"a": 1})
	if err != nil {
		t.Fatal(err)
	}
	if data["topic"] != "billing" || data["urgent"] != true {
		t.Fatalf("not merged: %v", data)
	}

	g := newFakeLLM(t, "```json\n{\"topic\":\"x\"}\n```")
	data, err = run(t, "ai_prompt", g.config(map[string]any{"prompt": "x", "outputMode": "json", "targetField": "ai"}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if nested, _ := data["ai"].(map[string]any); nested["topic"] != "x" {
		t.Fatalf("not nested (fences must be tolerated): %v", data)
	}
}

func TestPrompt_UsageField(t *testing.T) {
	f := newFakeLLM(t, "ok")
	data, err := run(t, "ai_prompt", f.config(map[string]any{"prompt": "x", "usageField": "ai_usage"}), nil)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := data["ai_usage"].(map[string]any)
	if u["input_tokens"] != int64(10) || u["output_tokens"] != int64(5) || u["model"] != "served-model" {
		t.Fatalf("usage = %v", data["ai_usage"])
	}
}

// Every call's usage is added to the message's metadata, whether or not the
// node names a usageField: the trace step a node records carries the message's
// metadata, so this is how a run's token totals reach its trace.
func TestUsageAccumulatesInMetadata(t *testing.T) {
	f := newFakeLLM(t, "ok")
	tf, _ := transformer.Get("ai_prompt")
	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	for range 2 {
		if _, err := tf.Transform(t.Context(), msg, f.config(map[string]any{"prompt": "x"})); err != nil {
			t.Fatal(err)
		}
	}
	got := UsageOf(msg)
	if got.Calls != 2 || got.InputTokens != 20 || got.OutputTokens != 10 {
		t.Fatalf("usage = %+v, want 2 calls, 20 in, 10 out", got)
	}
	if v, _ := msg.Metadata()[MetaAIInputTokens]; v != "20" {
		t.Fatalf("metadata %s = %q", MetaAIInputTokens, v)
	}
}

func TestPrompt_APIKeyIsResolvedNotFromRowData(t *testing.T) {
	f := newFakeLLM(t, "ok")
	_, err := run(t, "ai_prompt", f.config(map[string]any{"prompt": "x", "apiKey": "{{.stolen}}"}), map[string]any{"stolen": "row-chosen-key"})
	if err != nil {
		t.Fatal(err)
	}
	if auth := f.headers[0].Get("Authorization"); strings.Contains(auth, "row-chosen-key") {
		t.Fatalf("row data chose the credential: %q", auth)
	}
}

func TestPrompt_ErrorsAreReturned(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"no such model"}}`))
	}))
	defer srv.Close()
	_, err := run(t, "ai_prompt", map[string]any{"provider": "openai_compatible", "baseUrl": srv.URL, "model": "m", "prompt": "x"}, nil)
	if err == nil || !strings.Contains(err.Error(), "no such model") {
		t.Fatalf("err = %v", err)
	}
	if _, err := run(t, "ai_prompt", map[string]any{"provider": "openai_compatible", "baseUrl": srv.URL, "model": "m"}, nil); err == nil {
		t.Fatal("a prompt node without a prompt must fail")
	}
}

func TestObserverSeesCalls(t *testing.T) {
	var mu sync.Mutex
	var got []llm.CallRecord
	SetObserver(func(_ context.Context, r llm.CallRecord) { mu.Lock(); got = append(got, r); mu.Unlock() })
	t.Cleanup(func() { SetObserver(nil) })

	f := newFakeLLM(t, "ok")
	if _, err := run(t, "ai_prompt", f.config(map[string]any{"prompt": "x"}), nil); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0].Usage.InputTokens != 10 {
		t.Fatalf("records = %+v", got)
	}
}

func TestProvidersAreCachedPerConnection(t *testing.T) {
	cfg := map[string]any{"provider": "ollama", "model": "m"}
	a, _, err := ProviderFor(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _, _ := ProviderFor(cfg, nil)
	if a != b {
		t.Error("the same connection should reuse one provider (and its concurrency limit)")
	}
	c, _, _ := ProviderFor(map[string]any{"provider": "ollama", "model": "other"}, nil)
	if a == c {
		t.Error("different connections must not share a provider")
	}
}

// With memory on, ai_prompt sends the conversation's earlier exchanges ahead
// of the new message and remembers the new exchange; another conversation
// starts fresh.
func TestPrompt_ConversationMemory(t *testing.T) {
	f := newFakeLLM(t, "Hi Ada.", "Your name is Ada.", "I don't know your name.")
	store := state.NewMemoryStore()
	ctx := context.WithValue(context.WithValue(t.Context(), hermod.StateStoreKey, store), hermod.NodeIDKey, "chat")
	cfg := f.config(map[string]any{"prompt": "{{.text}}", "memory": map[string]any{"maxTurns": float64(5)}})
	tr, _ := transformer.Get("ai_prompt")

	say := func(conv, text string) {
		t.Helper()
		msg := message.AcquireMessage()
		defer message.ReleaseMessage(msg)
		msg.SetData("text", text)
		msg.SetMetadata(reply.MetaConversationID, conv)
		if _, err := tr.Transform(ctx, msg, cfg); err != nil {
			t.Fatal(err)
		}
	}
	say("c-1", "I am Ada")
	say("c-1", "What is my name?")
	say("c-2", "What is my name?")

	f.mu.Lock()
	defer f.mu.Unlock()
	roles := func(i int) []string {
		var out []string
		for _, m := range f.bodies[i]["messages"].([]any) {
			mm := m.(map[string]any)
			if mm["role"] == "system" {
				continue
			}
			out = append(out, mm["role"].(string)+":"+mm["content"].(string))
		}
		return out
	}
	if got := strings.Join(roles(1), "|"); got != "user:I am Ada|assistant:Hi Ada.|user:What is my name?" {
		t.Fatalf("second call sent %q", got)
	}
	if got := strings.Join(roles(2), "|"); got != "user:What is my name?" {
		t.Fatalf("another conversation sent %q", got)
	}
}

func TestPrompt_MemoryNeedsAStateStore(t *testing.T) {
	f := newFakeLLM(t, "x")
	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	msg.SetMetadata(reply.MetaConversationID, "c-1")
	tr, _ := transformer.Get("ai_prompt")
	_, err := tr.Transform(t.Context(), msg, f.config(map[string]any{"prompt": "hi", "memory": map[string]any{}}))
	if err == nil || !strings.Contains(err.Error(), "state store") {
		t.Fatalf("err = %v", err)
	}
	if f.calls() != 0 {
		t.Error("the model was called with a memory that could not be loaded")
	}
}
