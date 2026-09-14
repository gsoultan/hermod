package metis

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gsoultan/hermod"
)

// --- test doubles ------------------------------------------------------------

type mockMessage struct {
	id   string
	op   hermod.Operation
	data map[string]any
}

func (m *mockMessage) ID() string                     { return m.id }
func (m *mockMessage) Operation() hermod.Operation    { return m.op }
func (m *mockMessage) Table() string                  { return "orders" }
func (m *mockMessage) Schema() string                 { return "public" }
func (m *mockMessage) Before() []byte                 { return nil }
func (m *mockMessage) After() []byte                  { return nil }
func (m *mockMessage) Payload() []byte                { return nil }
func (m *mockMessage) Metadata() map[string]string    { return nil }
func (m *mockMessage) MetadataRef() map[string]string { return nil }
func (m *mockMessage) Data() map[string]any           { return m.data }
func (m *mockMessage) DataRef() map[string]any        { return m.data }
func (m *mockMessage) SetMetadata(string, string)     {}
func (m *mockMessage) SetData(string, any)            {}
func (m *mockMessage) Clone() hermod.Message          { return m }
func (m *mockMessage) ToMap() map[string]any          { return m.data }
func (m *mockMessage) ClearPayloads()                 {}
func (m *mockMessage) Retain()                        {}
func (m *mockMessage) Release()                       {}

func testMessage() *mockMessage {
	return &mockMessage{
		id: "ord-1",
		op: hermod.OpCreate,
		data: map[string]any{
			"order_id": "A-77",
			"customer": "Ada",
			"amount":   900.0,
		},
	}
}

// memStore records what happened to each key, so a test can tell "released, a
// retry may take it" from "still claimed, a retry must not run the process
// again".
type memStore struct {
	mu       sync.Mutex
	claimed  map[string]bool
	sent     map[string]bool
	released []string
}

func newMemStore() *memStore {
	return &memStore{claimed: map[string]bool{}, sent: map[string]bool{}}
}

func (s *memStore) Claim(_ context.Context, key string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.claimed[key] {
		return false, nil
	}
	s.claimed[key] = true
	return true, nil
}

func (s *memStore) MarkSent(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent[key] = true
	return nil
}

func (s *memStore) Release(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sent[key] {
		return nil
	}
	delete(s.claimed, key)
	s.released = append(s.released, key)
	return nil
}

func (s *memStore) heldKeys() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.claimed)
}

// call is one request the fake engine received.
type call struct {
	path string
	auth string
	body map[string]any
}

// engine stands in for a Metis server. `respond` decides what each non-login
// request gets; returning false falls through to the default success body.
type engine struct {
	srv    *httptest.Server
	mu     sync.Mutex
	calls  []call
	logins int

	respond func(w http.ResponseWriter, path string) bool
}

func newEngine(t *testing.T) *engine {
	t.Helper()
	e := &engine{}
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)

		e.mu.Lock()
		e.calls = append(e.calls, call{path: r.URL.Path, auth: r.Header.Get("Authorization"), body: body})
		if r.URL.Path == "/api/v1/login" {
			e.logins++
		}
		respond := e.respond
		e.mu.Unlock()

		if r.URL.Path == "/api/v1/login" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"token":"tok-1"}`))
			return
		}
		if respond != nil && respond(w, r.URL.Path) {
			return
		}

		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/process/start":
			_, _ = w.Write([]byte(`{"instance_id":"inst-9"}`))
		case "/api/v1/projects":
			_, _ = w.Write([]byte(`{"projects":[{"id":"proj-1","name":"Ops"}]}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(e.srv.Close)
	return e
}

func (e *engine) url() string { return e.srv.URL }

// lastBody is the decoded body of the most recent call to path.
func (e *engine) lastBody(t *testing.T, path string) map[string]any {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	for i := len(e.calls) - 1; i >= 0; i-- {
		if e.calls[i].path == path {
			return e.calls[i].body
		}
	}
	t.Fatalf("no call to %s; got %v", path, e.paths())
	return nil
}

func (e *engine) paths() []string {
	out := make([]string, 0, len(e.calls))
	for _, c := range e.calls {
		out = append(out, c.path)
	}
	return out
}

func (e *engine) countPath(path string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := 0
	for _, c := range e.calls {
		if c.path == path {
			n++
		}
	}
	return n
}

func (e *engine) loginCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.logins
}

func baseConfig(url string) Config {
	return Config{
		BaseURL:       url,
		Token:         "tok-static",
		ProjectID:     "proj-1",
		Action:        ActionStartProcess,
		DefinitionKey: "order-fulfilment",
	}
}

// --- configuration -----------------------------------------------------------

func TestNew_NamesTheMissingSetting(t *testing.T) {
	tests := []struct {
		name string
		mut  func(*Config)
		want string
	}{
		{"no base url", func(c *Config) { c.BaseURL = "" }, "base url"},
		{"no project", func(c *Config) { c.ProjectID = "" }, "project id"},
		{"no definition key", func(c *Config) { c.DefinitionKey = "" }, "definition key"},
		{"unknown action", func(c *Config) { c.Action = "nope" }, "action"},
		{
			"send_message without a name",
			func(c *Config) { c.Action = ActionSendMessage; c.DefinitionKey = "" },
			"message name",
		},
		{
			"broadcast_signal without a name",
			func(c *Config) { c.Action = ActionBroadcastSignal; c.DefinitionKey = "" },
			"signal name",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := baseConfig("https://bpm.example.com")
			tc.mut(&cfg)
			_, err := New(cfg, nil)
			if err == nil {
				t.Fatal("New accepted a config it cannot work with")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not name %q", err, tc.want)
			}
		})
	}
}

func TestNew_RefusesPlaintextOffLoopback(t *testing.T) {
	cfg := baseConfig("http://bpm.example.com")
	if _, err := New(cfg, nil); err == nil {
		t.Fatal("plaintext to a remote host was accepted; the token travels in a header")
	}

	cfg = baseConfig("http://127.0.0.1:8080")
	if _, err := New(cfg, nil); err != nil {
		t.Fatalf("plaintext to loopback should be allowed: %v", err)
	}
}

func TestNew_RequiresSomeCredential(t *testing.T) {
	cfg := baseConfig("https://bpm.example.com")
	cfg.Token = ""
	if _, err := New(cfg, nil); err == nil {
		t.Fatal("a sink with neither token nor username was accepted")
	}
}

// --- the happy path -----------------------------------------------------------

func TestWrite_StartsAProcessWithTheMessageAsVariables(t *testing.T) {
	e := newEngine(t)
	s, err := New(baseConfig(e.url()), nil)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.Write(context.Background(), testMessage()); err != nil {
		t.Fatalf("Write: %v", err)
	}

	body := e.lastBody(t, "/api/v1/process/start")
	if body["project_id"] != "proj-1" {
		t.Errorf("project_id = %v, want proj-1", body["project_id"])
	}
	if body["definition_key"] != "order-fulfilment" {
		t.Errorf("definition_key = %v, want order-fulfilment", body["definition_key"])
	}

	vars, ok := body["variables"].(map[string]any)
	if !ok {
		t.Fatalf("variables is %T, want an object", body["variables"])
	}
	if vars["order_id"] != "A-77" {
		t.Errorf("variables.order_id = %v, want A-77", vars["order_id"])
	}
	if vars["amount"] != 900.0 {
		t.Errorf("variables.amount = %v, want 900", vars["amount"])
	}
	if vars["operation"] != string(hermod.OpCreate) {
		t.Errorf("variables.operation = %v, want %v", vars["operation"], hermod.OpCreate)
	}

	if got := s.LastInstanceID(); got != "inst-9" {
		t.Errorf("LastInstanceID = %q, want inst-9", got)
	}
}

// A CDC row has its own columns, and one of them may be called `table` or `id`.
// The envelope is written first so the row wins — writing it last silently
// replaced a row's own column with Hermod's metadata.
func TestWrite_RowColumnsWinOverEnvelopeFields(t *testing.T) {
	e := newEngine(t)
	s, err := New(baseConfig(e.url()), nil)
	if err != nil {
		t.Fatal(err)
	}

	msg := testMessage()
	msg.data["table"] = "the row's own table column"
	msg.data["id"] = "the row's own id column"

	if err := s.Write(context.Background(), msg); err != nil {
		t.Fatalf("Write: %v", err)
	}

	vars := e.lastBody(t, "/api/v1/process/start")["variables"].(map[string]any)
	if vars["table"] != "the row's own table column" {
		t.Errorf("the envelope overwrote the row's `table` column: %v", vars["table"])
	}
	if vars["id"] != "the row's own id column" {
		t.Errorf("the envelope overwrote the row's `id` column: %v", vars["id"])
	}
}

func TestWrite_TemplatesTheDefinitionKey(t *testing.T) {
	e := newEngine(t)
	cfg := baseConfig(e.url())
	cfg.DefinitionKey = "{{.table}}-intake"
	s, err := New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.Write(context.Background(), testMessage()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := e.lastBody(t, "/api/v1/process/start")["definition_key"]; got != "orders-intake" {
		t.Errorf("definition_key = %v, want orders-intake", got)
	}
}

func TestWrite_SendsAMessageWithACorrelationKey(t *testing.T) {
	e := newEngine(t)
	cfg := baseConfig(e.url())
	cfg.Action = ActionSendMessage
	cfg.DefinitionKey = ""
	cfg.MessageName = "payment-received"
	cfg.CorrelationKey = "{{.order_id}}"
	s, err := New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.Write(context.Background(), testMessage()); err != nil {
		t.Fatalf("Write: %v", err)
	}

	body := e.lastBody(t, "/api/v1/processes/message")
	if body["message_name"] != "payment-received" {
		t.Errorf("message_name = %v", body["message_name"])
	}
	if body["correlation_key"] != "A-77" {
		t.Errorf("correlation_key = %v, want A-77", body["correlation_key"])
	}
}

func TestWrite_BroadcastsASignal(t *testing.T) {
	e := newEngine(t)
	cfg := baseConfig(e.url())
	cfg.Action = ActionBroadcastSignal
	cfg.DefinitionKey = ""
	cfg.SignalName = "price-list-changed"
	s, err := New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.Write(context.Background(), testMessage()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := e.lastBody(t, "/api/v1/processes/signal")["signal_name"]; got != "price-list-changed" {
		t.Errorf("signal_name = %v", got)
	}
}

func TestWrite_VariableFieldsSelectASubset(t *testing.T) {
	e := newEngine(t)
	cfg := baseConfig(e.url())
	cfg.VariableFields = []string{"order_id", "amount"}
	s, err := New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.Write(context.Background(), testMessage()); err != nil {
		t.Fatalf("Write: %v", err)
	}

	vars := e.lastBody(t, "/api/v1/process/start")["variables"].(map[string]any)
	if len(vars) != 2 {
		t.Fatalf("variables = %v, want only the two named fields", vars)
	}
	if vars["order_id"] != "A-77" || vars["amount"] != 900.0 {
		t.Errorf("variables = %v", vars)
	}
}

func TestWrite_NilMessageIsNotAnError(t *testing.T) {
	e := newEngine(t)
	s, err := New(baseConfig(e.url()), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Write(context.Background(), nil); err != nil {
		t.Fatalf("a nil message should be ignored, got %v", err)
	}
	if n := e.countPath("/api/v1/process/start"); n != 0 {
		t.Errorf("a nil message started %d processes", n)
	}
}

func TestWriteBatch_StartsOnePerMessage(t *testing.T) {
	e := newEngine(t)
	s, err := New(baseConfig(e.url()), nil)
	if err != nil {
		t.Fatal(err)
	}

	msgs := []hermod.Message{testMessage(), nil, testMessage()}
	if err := s.WriteBatch(context.Background(), msgs); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}
	if n := e.countPath("/api/v1/process/start"); n != 2 {
		t.Errorf("started %d processes, want 2 (the nil skipped)", n)
	}
}

// --- authentication -----------------------------------------------------------

func TestWrite_LogsInOnceAndReusesTheToken(t *testing.T) {
	e := newEngine(t)
	cfg := baseConfig(e.url())
	cfg.Token = ""
	cfg.Username = "svc"
	cfg.Password = "secret"
	s, err := New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}

	for range 3 {
		if err := s.Write(context.Background(), testMessage()); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	if got := e.loginCount(); got != 1 {
		t.Errorf("logged in %d times, want 1 — the token is meant to be reused", got)
	}
	if got := e.lastBody(t, "/api/v1/process/start"); got == nil {
		t.Fatal("no process was started")
	}
}

// A token expires. The 401 that follows is the server stating it did nothing, so
// logging in again and repeating the call cannot start the process twice.
func TestWrite_LogsInAgainWhenTheTokenExpires(t *testing.T) {
	e := newEngine(t)
	var refused bool
	var mu sync.Mutex
	e.respond = func(w http.ResponseWriter, path string) bool {
		if path != "/api/v1/process/start" {
			return false
		}
		mu.Lock()
		defer mu.Unlock()
		if !refused {
			refused = true
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"token expired"}`))
			return true
		}
		return false
	}

	cfg := baseConfig(e.url())
	cfg.Token = ""
	cfg.Username = "svc"
	cfg.Password = "secret"
	s, err := New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.Write(context.Background(), testMessage()); err != nil {
		t.Fatalf("Write should have recovered from an expired token: %v", err)
	}
	if got := e.loginCount(); got != 2 {
		t.Errorf("logged in %d times, want 2 — one at the start, one after the 401", got)
	}
	if got := e.countPath("/api/v1/process/start"); got != 2 {
		t.Errorf("start attempted %d times, want 2", got)
	}
}

// Without credentials there is nothing to log in again with, so the 401 must
// surface rather than being retried into the same wall.
func TestWrite_DoesNotRetryA401WithAStaticToken(t *testing.T) {
	e := newEngine(t)
	e.respond = func(w http.ResponseWriter, path string) bool {
		if path != "/api/v1/process/start" {
			return false
		}
		w.WriteHeader(http.StatusUnauthorized)
		return true
	}

	s, err := New(baseConfig(e.url()), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Write(context.Background(), testMessage()); err == nil {
		t.Fatal("a 401 with a static token should be an error")
	}
	if got := e.countPath("/api/v1/process/start"); got != 1 {
		t.Errorf("start attempted %d times, want 1 — there is no second credential to try", got)
	}
}

// --- retries and duplicate process instances ----------------------------------

func TestWrite_StatedRefusalReleasesTheClaim(t *testing.T) {
	e := newEngine(t)
	e.respond = func(w http.ResponseWriter, path string) bool {
		if path != "/api/v1/process/start" {
			return false
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"no such definition key"}`))
		return true
	}

	store := newMemStore()
	s, err := New(baseConfig(e.url()), nil)
	if err != nil {
		t.Fatal(err)
	}
	s.EnableIdempotency(true)
	s.SetIdempotencyStore(store)

	if err := s.Write(context.Background(), testMessage()); err == nil {
		t.Fatal("a refused start should be an error")
	}
	if len(store.released) != 1 {
		t.Errorf("released %v, want the claim released — the engine said it started nothing", store.released)
	}
	if store.heldKeys() != 0 {
		t.Error("a stated refusal left the key claimed, so a corrected retry is suppressed")
	}
}

// A 5xx is an answer, but it is not a statement that nothing happened: the
// engine may have created the instance and then faltered. That makes it an
// unknown outcome here, unlike in a sink whose refusals are all stated.
func TestWrite_ServerErrorKeepsTheClaim(t *testing.T) {
	e := newEngine(t)
	e.respond = func(w http.ResponseWriter, path string) bool {
		if path != "/api/v1/process/start" {
			return false
		}
		w.WriteHeader(http.StatusInternalServerError)
		return true
	}

	store := newMemStore()
	s, err := New(baseConfig(e.url()), nil)
	if err != nil {
		t.Fatal(err)
	}
	s.EnableIdempotency(true)
	s.SetIdempotencyStore(store)

	err = s.Write(context.Background(), testMessage())
	if err == nil {
		t.Fatal("a 500 should be an error")
	}
	if len(store.released) != 0 {
		t.Error("a 500 released the claim; the engine may have started the process before it failed")
	}
	if store.heldKeys() != 1 {
		t.Error("the claim should be kept so a retry cannot start a second instance")
	}
	if !strings.Contains(err.Error(), "not known") {
		t.Errorf("the error should say the outcome is unknown: %v", err)
	}
}

func TestWrite_UnknownOutcomeKeepsTheClaim(t *testing.T) {
	e := newEngine(t)
	s, err := New(baseConfig(e.url()), nil)
	if err != nil {
		t.Fatal(err)
	}
	store := newMemStore()
	s.EnableIdempotency(true)
	s.SetIdempotencyStore(store)

	// Close the server so the request fails in transport, with no answer at all.
	e.srv.Close()

	if err := s.Write(context.Background(), testMessage()); err == nil {
		t.Fatal("a dead server should be an error")
	}
	if len(store.released) != 0 {
		t.Error("a transport failure released the claim; a retry can now start a second instance")
	}
	if store.heldKeys() != 1 {
		t.Error("the claim should be kept")
	}
}

func TestWrite_UnknownOutcomeWithoutAStoreSaysSo(t *testing.T) {
	e := newEngine(t)
	s, err := New(baseConfig(e.url()), nil)
	if err != nil {
		t.Fatal(err)
	}
	e.srv.Close()

	err = s.Write(context.Background(), testMessage())
	if err == nil {
		t.Fatal("a dead server should be an error")
	}
	if !strings.Contains(err.Error(), "idempotency") {
		t.Errorf("with no claim to hold, the error should say a retry may duplicate: %v", err)
	}
}

func TestWrite_SuppressesADuplicate(t *testing.T) {
	e := newEngine(t)
	s, err := New(baseConfig(e.url()), nil)
	if err != nil {
		t.Fatal(err)
	}
	store := newMemStore()
	s.EnableIdempotency(true)
	s.SetIdempotencyStore(store)

	msg := testMessage()
	if err := s.Write(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(context.Background(), msg); err != nil {
		t.Fatal(err)
	}

	if n := e.countPath("/api/v1/process/start"); n != 1 {
		t.Errorf("started %d processes for one message, want 1", n)
	}
	dedup, _ := s.LastWriteIdempotent()
	if !dedup {
		t.Error("the second write should report itself as deduplicated")
	}
}

func TestWrite_IdempotencyKeyTemplateGroupsByBusinessKey(t *testing.T) {
	e := newEngine(t)
	s, err := New(baseConfig(e.url()), nil)
	if err != nil {
		t.Fatal(err)
	}
	store := newMemStore()
	s.EnableIdempotency(true)
	s.SetIdempotencyStore(store)
	s.SetIdempotencyKeyTemplate("order-{{.order_id}}")

	first := testMessage()
	second := testMessage()
	second.id = "ord-2" // a different message, the same order
	if err := s.Write(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(context.Background(), second); err != nil {
		t.Fatal(err)
	}

	if n := e.countPath("/api/v1/process/start"); n != 1 {
		t.Errorf("started %d processes, want 1 — both messages carry order A-77", n)
	}
}

// --- reachability --------------------------------------------------------------

func TestPing_ReadsRatherThanStartingAProcess(t *testing.T) {
	e := newEngine(t)
	s, err := New(baseConfig(e.url()), nil)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if n := e.countPath("/api/v1/projects"); n != 1 {
		t.Errorf("Ping made %d project listings, want 1", n)
	}
	if n := e.countPath("/api/v1/process/start"); n != 0 {
		t.Error("Ping started a process; a health check must not run somebody's business process")
	}
}

func TestPing_FailsOnABadToken(t *testing.T) {
	e := newEngine(t)
	e.respond = func(w http.ResponseWriter, path string) bool {
		if path != "/api/v1/projects" {
			return false
		}
		w.WriteHeader(http.StatusUnauthorized)
		return true
	}

	s, err := New(baseConfig(e.url()), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Ping(context.Background()); err == nil {
		t.Fatal("Ping should fail when the credentials are refused")
	}
}

func TestClose_IsIdempotent(t *testing.T) {
	e := newEngine(t)
	s, err := New(baseConfig(e.url()), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}
