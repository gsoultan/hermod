package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/engine/registry"
	"github.com/gsoultan/hermod/internal/factory"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/buffer"
	"github.com/gsoultan/hermod/pkg/comm/reply"
	pkgengine "github.com/gsoultan/hermod/pkg/engine"
)

// The chat trigger.
//
// A chat source is an endpoint that takes one message of a conversation and
// answers with what the workflow made of it, so a chat widget, a Slack app or a
// Telegram bot can sit in front of an AI Prompt node with memory. These tests
// run a request through everything between the caller and the answer: the auth
// middleware, the handler, a source built from its stored configuration by the
// factory, a real engine, and a router standing in for the AI node.

// sourcesOnly is a store that holds the given sources.
type sourcesOnly struct {
	storage.Storage
	sources []storage.Source
}

func (s sourcesOnly) ListSources(context.Context, storage.CommonFilter) ([]storage.Source, int, error) {
	return s.sources, len(s.sources), nil
}

// unreadable is a store whose sources cannot be listed.
type unreadable struct{ storage.Storage }

func (unreadable) ListSources(context.Context, storage.CommonFilter) ([]storage.Source, int, error) {
	return nil, 0, errors.New("the sources table is unavailable")
}

type nopSink struct{}

func (nopSink) Write(context.Context, hermod.Message) error { return nil }
func (nopSink) Ping(context.Context) error                  { return nil }
func (nopSink) Close() error                                { return nil }

type failingSink struct{}

func (failingSink) Write(context.Context, hermod.Message) error {
	return errors.Join(hermod.ErrPermanent, errors.New("provider key sk-live-123 was rejected"))
}
func (failingSink) Ping(context.Context) error { return nil }
func (failingSink) Close() error               { return nil }

// workflow is what the running workflow saw.
type workflow struct {
	mu   sync.Mutex
	seen []map[string]any
	meta []map[string]string
}

func (w *workflow) last(t *testing.T) (map[string]any, map[string]string) {
	t.Helper()
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.seen) == 0 {
		t.Fatal("the workflow saw no message")
	}
	return w.seen[len(w.seen)-1], w.meta[len(w.meta)-1]
}

// chat is one chat source, its running workflow and the server in front of it.
type chat struct {
	handler  http.Handler
	workflow *workflow
	h        *ChatHandler
	// src is the source as the workflow holds it.
	src hermod.Source
}

type chatOptions struct {
	// noEngine leaves the source's path held with nobody reading it.
	noEngine bool
	sink     hermod.Sink
	// answer sets the reply on the message, as an AI Prompt node would. The
	// default writes ai_output = "echo: <message>".
	answer func(hermod.Message)
	// registry resolves secret references in the source's config.
	registry *registry.Registry
}

// newChat stores a chat source with the given config, builds it the way a
// workflow does and runs it.
func newChat(t *testing.T, path string, config map[string]string, opts chatOptions) *chat {
	t.Helper()
	fullPath := "/api/chat/" + path
	cfg := map[string]string{"path": fullPath}
	for k, v := range config {
		cfg[k] = v
	}
	src, err := factory.CreateSource(factory.SourceConfig{ID: "src-" + path, Type: "chat", Config: cfg})
	if err != nil {
		t.Fatalf("building the chat source: %v", err)
	}
	wf := &workflow{}
	if opts.noEngine {
		t.Cleanup(func() { _ = src.Close() })
	} else {
		sink := opts.sink
		if sink == nil {
			sink = nopSink{}
		}
		answer := opts.answer
		if answer == nil {
			answer = func(m hermod.Message) { m.SetData("ai_output", "echo: "+asString(m.Data()["message"])) }
		}
		eng := pkgengine.NewEngine(src, []hermod.Sink{sink}, buffer.NewRingBuffer(8))
		eng.SetConfig(pkgengine.Config{MaxRetries: 1, RetryInterval: 10 * time.Millisecond, StatusInterval: time.Second})
		eng.SetRouter(func(_ context.Context, msg hermod.Message) ([]pkgengine.RoutedMessage, error) {
			out := msg.Clone()
			wf.mu.Lock()
			data := map[string]any{}
			for k, v := range out.Data() {
				data[k] = v
			}
			meta := map[string]string{}
			for k, v := range out.Metadata() {
				meta[k] = v
			}
			wf.seen = append(wf.seen, data)
			wf.meta = append(wf.meta, meta)
			wf.mu.Unlock()
			answer(out)
			return []pkgengine.RoutedMessage{{SinkIndex: 0, Message: out}}, nil
		})
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = eng.Start(ctx)
		}()
		t.Cleanup(func() {
			cancel()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("the engine did not stop")
			}
		})
	}

	base := &handlers.Handler{
		Storage:  sourcesOnly{sources: []storage.Source{{ID: "src-" + path, Type: "chat", VHost: "support", Config: cfg}}},
		Registry: opts.registry,
	}
	h := NewChatHandler(base)
	mux := http.NewServeMux()
	h.RegisterChatRoutes(mux)
	// Through the real auth middleware: a chat endpoint is called by people
	// and bots with no Hermod session.
	return &chat{handler: base.AuthMiddleware(mux), workflow: wf, h: h, src: src}
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

// post sends body to the chat endpoint at path.
func (c *chat) post(t *testing.T, path, body string, header http.Header) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, strings.NewReader(body))
	req.RemoteAddr = "192.0.2.1:1234"
	for k, v := range header {
		req.Header[k] = v
	}
	c.handler.ServeHTTP(rec, req)
	return rec
}

type chatReply struct {
	ID             string `json:"id"`
	Status         string `json:"status"`
	ConversationID string `json:"conversation_id"`
	Reply          string `json:"reply"`
	Error          string `json:"error"`
}

func decodeReply(t *testing.T, rec *httptest.ResponseRecorder) chatReply {
	t.Helper()
	var r chatReply
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
		t.Fatalf("the response is not JSON: %v\n%s", err, rec.Body.String())
	}
	return r
}

var keyed = map[string]string{"api_key": "sesame"}

func withKey() http.Header { return http.Header{"X-Api-Key": {"sesame"}} }

func TestAChatAnswersWithTheWorkflowsReply(t *testing.T) {
	c := newChat(t, "support", keyed, chatOptions{})
	rec := c.post(t, "/api/chat/support", `{"conversation_id":"conv-1","message":"hello"}`, withKey())

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", rec.Code, rec.Body.String())
	}
	r := decodeReply(t, rec)
	if r.Reply != "echo: hello" || r.ConversationID != "conv-1" || r.Status != "delivered" || r.ID == "" {
		t.Errorf("the caller was told %+v", r)
	}
	if strings.Contains(rec.Body.String(), `"record"`) {
		t.Errorf("the answer carries the whole record: %s", rec.Body.String())
	}
}

// The message the workflow sees has the request's fields, and the conversation
// id where an AI Prompt node's memory looks for it.
func TestAChatMessageCarriesTheConversation(t *testing.T) {
	c := newChat(t, "fields", keyed, chatOptions{})
	rec := c.post(t, "/api/chat/fields",
		`{"conversation_id":"conv-2","message":"where is my order?","user":"ana","metadata":{"page":"/orders"}}`, withKey())
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	data, meta := c.workflow.last(t)
	if data["conversation_id"] != "conv-2" || data["message"] != "where is my order?" || data["user"] != "ana" {
		t.Errorf("the workflow saw %v", data)
	}
	if md, _ := data["metadata"].(map[string]any); md["page"] != "/orders" {
		t.Errorf("the workflow saw metadata %v", data["metadata"])
	}
	if meta[reply.MetaConversationID] != "conv-2" || meta["chat_platform"] != "web" || meta["chat_path"] != "/api/chat/fields" {
		t.Errorf("the message's metadata was %v", meta)
	}
}

func TestAChatStartsAConversationWhenNoneIsNamed(t *testing.T) {
	c := newChat(t, "fresh", keyed, chatOptions{})
	rec := c.post(t, "/api/chat/fresh", `{"message":"hi"}`, withKey())
	r := decodeReply(t, rec)
	if !reply.ValidConversationID(r.ConversationID) {
		t.Fatalf("no conversation id was issued: %+v", r)
	}
	if data, _ := c.workflow.last(t); data["conversation_id"] != r.ConversationID {
		t.Errorf("the workflow saw %v, the caller was given %q", data["conversation_id"], r.ConversationID)
	}
}

func TestTheReplyFieldIsConfigurable(t *testing.T) {
	c := newChat(t, "nested", map[string]string{"api_key": "sesame", "reply_field": "answer.text"}, chatOptions{
		answer: func(m hermod.Message) { m.SetData("answer", map[string]any{"text": "nested hello"}) },
	})
	rec := c.post(t, "/api/chat/nested", `{"conversation_id":"c","message":"hi"}`, withKey())
	if r := decodeReply(t, rec); r.Reply != "nested hello" {
		t.Errorf("the caller was told %+v", r)
	}
}

// A chat endpoint is a way to spend the operator's model budget, so one with
// no credential configured answers nobody rather than everybody.
func TestAChatRefusesACallerWithoutItsCredential(t *testing.T) {
	widget := map[string]string{
		"api_key":         "sesame",
		"widget_key":      "wk_public",
		"allowed_origins": "https://shop.example.com, https://help.example.com",
	}
	tests := []struct {
		name   string
		config map[string]string
		url    string
		header http.Header
		want   int
	}{
		{"no credential sent", keyed, "/api/chat/%s", nil, http.StatusUnauthorized},
		{"wrong API key", keyed, "/api/chat/%s", http.Header{"X-Api-Key": {"guess"}}, http.StatusUnauthorized},
		{"no credential configured", map[string]string{}, "/api/chat/%s", http.Header{"X-Api-Key": {""}}, http.StatusUnauthorized},
		{"a key sent to a source with none", map[string]string{"widget_key": "wk"}, "/api/chat/%s", http.Header{"X-Api-Key": {"anything"}}, http.StatusUnauthorized},
		{"wrong widget key", widget, "/api/chat/%s?widget_key=wk_other", http.Header{"Origin": {"https://shop.example.com"}}, http.StatusUnauthorized},
		{"widget key from another origin", widget, "/api/chat/%s?widget_key=wk_public", http.Header{"Origin": {"https://evil.example.net"}}, http.StatusForbidden},
		{"widget key with no origin", widget, "/api/chat/%s?widget_key=wk_public", nil, http.StatusForbidden},
		{"widget key with no allow-list", map[string]string{"widget_key": "wk_public"}, "/api/chat/%s?widget_key=wk_public", http.Header{"Origin": {"https://shop.example.com"}}, http.StatusForbidden},
		{"widget key from an allowed origin", widget, "/api/chat/%s?widget_key=wk_public", http.Header{"Origin": {"https://help.example.com"}}, http.StatusOK},
		{"the API key", widget, "/api/chat/%s", withKey(), http.StatusOK},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := "auth-" + string(rune('a'+i))
			c := newChat(t, path, tt.config, chatOptions{})
			url := strings.Replace(tt.url, "%s", path, 1)
			rec := c.post(t, url, `{"conversation_id":"c","message":"hi"}`, tt.header)
			if rec.Code != tt.want {
				t.Errorf("status %d, want %d: %s", rec.Code, tt.want, rec.Body.String())
			}
		})
	}
}

// The widget runs on the operator's site, so the answer has to be readable
// there, and only there.
func TestAWidgetAnswerIsReadableOnlyByItsAllowedOrigins(t *testing.T) {
	c := newChat(t, "cors", map[string]string{"widget_key": "wk", "allowed_origins": "https://shop.example.com"}, chatOptions{})

	rec := c.post(t, "/api/chat/cors?widget_key=wk", `{"conversation_id":"c","message":"hi"}`, http.Header{"Origin": {"https://shop.example.com"}})
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://shop.example.com" {
		t.Errorf("Access-Control-Allow-Origin = %q", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("a widget answer allows credentials: %q", got)
	}

	rec = c.post(t, "/api/chat/cors?widget_key=wk", `{"conversation_id":"c","message":"hi"}`, http.Header{"Origin": {"https://evil.example.net"}})
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("another origin was allowed to read the answer: %q", got)
	}
}

func TestAChatRefusesABadRequest(t *testing.T) {
	long := strings.Repeat("a", 4<<10+1)
	tests := []struct {
		name, body string
		want       int
	}{
		{"not JSON", `hello`, http.StatusBadRequest},
		{"no message", `{"conversation_id":"c"}`, http.StatusBadRequest},
		{"a blank message", `{"conversation_id":"c","message":"   "}`, http.StatusBadRequest},
		{"a message over the limit", `{"conversation_id":"c","message":"` + long + `"}`, http.StatusBadRequest},
		{"an invalid conversation id", `{"conversation_id":"../../etc","message":"hi"}`, http.StatusBadRequest},
		{"metadata that is not an object", `{"conversation_id":"c","message":"hi","metadata":[1]}`, http.StatusBadRequest},
		{"a body over the limit", `{"conversation_id":"c","message":"hi","metadata":{"x":"` + strings.Repeat("b", 32<<10) + `"}}`, http.StatusRequestEntityTooLarge},
	}
	c := newChat(t, "bad", keyed, chatOptions{noEngine: true})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := c.post(t, "/api/chat/bad", tt.body, withKey())
			if rec.Code != tt.want {
				t.Errorf("status %d, want %d: %s", rec.Code, tt.want, rec.Body.String())
			}
		})
	}
}

func TestAChatIsRateLimitedPerCaller(t *testing.T) {
	c := newChat(t, "limited", map[string]string{"api_key": "sesame", "rate_limit": "2"}, chatOptions{})
	for i := range 2 {
		if rec := c.post(t, "/api/chat/limited", `{"conversation_id":"c","message":"hi"}`, withKey()); rec.Code != http.StatusOK {
			t.Fatalf("request %d: status %d: %s", i+1, rec.Code, rec.Body.String())
		}
	}
	if rec := c.post(t, "/api/chat/limited", `{"conversation_id":"c","message":"hi"}`, withKey()); rec.Code != http.StatusTooManyRequests {
		t.Errorf("the third request was answered %d, want 429", rec.Code)
	}
}

// The wait ran out with the message still the workflow's. That is not a
// failure, or a caller that retries would send the message twice.
func TestAChatSaysWhenItStoppedWaiting(t *testing.T) {
	c := newChat(t, "slow", map[string]string{"api_key": "sesame", "response_timeout": "50ms"}, chatOptions{noEngine: true})
	started := time.Now()
	rec := c.post(t, "/api/chat/slow", `{"conversation_id":"c","message":"hi"}`, withKey())
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status %d, want 202: %s", rec.Code, rec.Body.String())
	}
	if r := decodeReply(t, rec); r.Status != "pending" || r.ID == "" || r.ConversationID != "c" {
		t.Errorf("the caller was told %+v", r)
	}
	if waited := time.Since(started); waited > 3*time.Second {
		t.Errorf("held for %v with a 50ms timeout", waited)
	}
}

// The caller learns that the workflow failed, not why: the cause is the
// operator's to read in the run history, and may name a provider or a key.
func TestAChatFailureDoesNotLeakItsCause(t *testing.T) {
	c := newChat(t, "broken", keyed, chatOptions{sink: failingSink{}})
	rec := c.post(t, "/api/chat/broken", `{"conversation_id":"c","message":"hi"}`, withKey())
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status %d, want 502: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "sk-live") {
		t.Errorf("the answer leaks the failure's cause: %s", rec.Body.String())
	}
	if r := decodeReply(t, rec); r.Status != "failed" || r.Error == "" {
		t.Errorf("the caller was told %+v", r)
	}
}

func TestAnUnknownChatIsNotFound(t *testing.T) {
	c := newChat(t, "known", keyed, chatOptions{noEngine: true})
	if rec := c.post(t, "/api/chat/unknown", `{"message":"hi"}`, withKey()); rec.Code != http.StatusNotFound {
		t.Errorf("status %d, want 404", rec.Code)
	}
}

// secretsOf is a secret manager holding the given values.
type secretsOf map[string]string

func (s secretsOf) Get(_ context.Context, key string) (string, error) { return s[key], nil }

// A credential can be kept as a secret reference, like any connector's: the
// endpoint checks the secret's value, never the reference itself.
func TestAChatCredentialCanBeASecretReference(t *testing.T) {
	reg := registry.NewRegistry(nil)
	reg.SetSecretManager(secretsOf{"CHAT_KEY": "resolved-key"})
	c := newChat(t, "secret-ref", map[string]string{"api_key": "secret:CHAT_KEY"}, chatOptions{registry: reg})

	if rec := c.post(t, "/api/chat/secret-ref", `{"conversation_id":"c","message":"hi"}`, http.Header{"X-Api-Key": {"resolved-key"}}); rec.Code != http.StatusOK {
		t.Errorf("the secret's value was answered %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if rec := c.post(t, "/api/chat/secret-ref", `{"conversation_id":"c","message":"hi"}`, http.Header{"X-Api-Key": {"secret:CHAT_KEY"}}); rec.Code != http.StatusUnauthorized {
		t.Errorf("the reference itself was answered %d, want 401", rec.Code)
	}

	// A reference to a secret that does not exist is no credential, not one
	// whose value is the reference.
	missing := newChat(t, "secret-missing", map[string]string{"api_key": "secret:NOT_SET"}, chatOptions{registry: reg})
	if rec := missing.post(t, "/api/chat/secret-missing", `{"conversation_id":"c","message":"hi"}`, http.Header{"X-Api-Key": {"secret:NOT_SET"}}); rec.Code != http.StatusUnauthorized {
		t.Errorf("an unresolved reference was accepted as the key: %d", rec.Code)
	}
}

// A store that cannot be read is not the same as a source with no credential.
func TestAChatWhoseCredentialsCannotBeReadRefuses(t *testing.T) {
	mux := http.NewServeMux()
	NewChatHandler(&handlers.Handler{Storage: unreadable{}}).RegisterChatRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/chat/x", strings.NewReader(`{"message":"hi"}`)))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status %d, want 503", rec.Code)
	}
}
