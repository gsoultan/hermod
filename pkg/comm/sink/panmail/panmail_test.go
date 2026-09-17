package panmail

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
)

// --- test doubles ------------------------------------------------------------

type mockMessage struct {
	id   string
	data map[string]any
}

func (m *mockMessage) ID() string                     { return m.id }
func (m *mockMessage) Operation() hermod.Operation    { return hermod.OpCreate }
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
		data: map[string]any{
			"email": "buyer@example.org",
			"name":  "Ada",
			"total": "42.00",
		},
	}
}

// memStore is an in-memory IdempotencyStore that records what happened to each
// key, so a test can tell "released, a retry may take it" from "still claimed,
// a retry must not re-send".
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

// gateway stands in for panmail, speaking the one Connect procedure the SDK
// posts to. `respond` decides what each request gets.
func gateway(t *testing.T, respond func(w http.ResponseWriter, body []byte)) (*httptest.Server, *[]map[string]any) {
	t.Helper()
	var mu sync.Mutex
	received := make([]map[string]any, 0, 2)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/panmail.v1.EmailService/SendEmail" {
			t.Errorf("posted to %s, want the SendEmail procedure", r.URL.Path)
		}
		if got := r.Header.Get("X-API-Key"); got != "key-123" {
			t.Errorf("X-API-Key = %q, want key-123", got)
		}
		body := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(body)

		var decoded map[string]any
		if err := json.Unmarshal(body, &decoded); err == nil {
			mu.Lock()
			received = append(received, decoded)
			mu.Unlock()
		}
		respond(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, &received
}

func accepted(w http.ResponseWriter, _ []byte) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"messageId":"msg-9","status":"EMAIL_EVENT_TYPE_PENDING"}`))
}

func baseConfig(url string) Config {
	return Config{
		BaseURL:    url,
		APIKey:     "key-123",
		ProviderID: "prov-1",
		From:       "noreply@example.com",
		To:         []string{"{{.email}}"},
		Subject:    "Order {{.id}} for {{.name}}",
		HTML:       "<p>Thanks, {{.name}} — {{.total}}</p>",
	}
}

// --- configuration -----------------------------------------------------------

func TestNew_NamesTheMissingSetting(t *testing.T) {
	for _, tc := range []struct {
		name  string
		mutex func(*Config)
		want  string
	}{
		{"no base url", func(c *Config) { c.BaseURL = "" }, "base url"},
		{"no api key", func(c *Config) { c.APIKey = "" }, "api key"},
		{"no provider id", func(c *Config) { c.ProviderID = "" }, "provider id"},
		{"no from", func(c *Config) { c.From = "" }, "from address"},
		{"no recipients", func(c *Config) { c.To = nil }, "recipient"},
		{"no body", func(c *Config) { c.HTML = ""; c.Text = ""; c.TemplateID = "" }, "body"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := baseConfig("http://127.0.0.1:1")
			tc.mutex(&cfg)
			_, err := New(cfg, nil)
			if err == nil {
				t.Fatalf("New succeeded with %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

func TestNew_RefusesPlaintextOffLoopback(t *testing.T) {
	cfg := baseConfig("http://mail.example.com")
	if _, err := New(cfg, nil); err == nil {
		t.Fatal("New accepted a plaintext non-loopback gateway, which sends the api key in the clear")
	}
}

// --- the happy path ----------------------------------------------------------

func TestWrite_RendersTheMessageIntoASend(t *testing.T) {
	srv, received := gateway(t, accepted)

	sink, err := New(baseConfig(srv.URL), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := sink.Write(context.Background(), testMessage()); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if len(*received) != 1 {
		t.Fatalf("gateway saw %d sends, want 1", len(*received))
	}
	sent := (*received)[0]

	for key, want := range map[string]string{
		"providerId": "prov-1",
		"from":       "noreply@example.com",
		"subject":    "Order ord-1 for Ada",
		"bodyHtml":   "<p>Thanks, Ada — 42.00</p>",
	} {
		if got, _ := sent[key].(string); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}

	to, _ := sent["to"].([]any)
	if len(to) != 1 || to[0] != "buyer@example.org" {
		t.Errorf("to = %v, want [buyer@example.org] rendered from the message", to)
	}
}

func TestWrite_RecordsTheMessageIDOnTheMessage(t *testing.T) {
	srv, _ := gateway(t, accepted)
	sink, err := New(baseConfig(srv.URL), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	msg := testMessage()
	if err := sink.Write(context.Background(), msg); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := sink.LastMessageID(); got != "msg-9" {
		t.Errorf("LastMessageID = %q, want msg-9 — it is the only handle on the message afterwards", got)
	}
}

// --- the part the SDK warns about --------------------------------------------

func TestWrite_StatedRefusalReleasesTheClaim(t *testing.T) {
	srv, _ := gateway(t, func(w http.ResponseWriter, _ []byte) {
		// A rate limit: the gateway said plainly it did not take the message.
		w.Header().Set("Retry-After", "30")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"code":"resource_exhausted","message":"over the send rate"}`))
	})

	store := newMemStore()
	sink, err := New(baseConfig(srv.URL), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	sink.EnableIdempotency(true)
	sink.SetIdempotencyStore(store)

	err = sink.Write(context.Background(), testMessage())
	if err == nil {
		t.Fatal("Write reported success for a refused send")
	}
	if len(store.released) != 1 {
		t.Fatalf("claim was not released after a stated refusal; a retry of a message "+
			"the gateway never took would be suppressed forever (released=%v)", store.released)
	}
	if store.heldKeys() != 0 {
		t.Errorf("%d keys still claimed after a refusal, want 0", store.heldKeys())
	}
}

func TestWrite_UnknownOutcomeKeepsTheClaim(t *testing.T) {
	// The connection dies mid-request: the gateway may or may not have taken
	// the message, and nothing in the response says which.
	srv, _ := gateway(t, func(http.ResponseWriter, []byte) {
		panic(http.ErrAbortHandler)
	})
	srv.Config.ErrorLog = nil

	store := newMemStore()
	sink, err := New(baseConfig(srv.URL), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	sink.EnableIdempotency(true)
	sink.SetIdempotencyStore(store)

	err = sink.Write(context.Background(), testMessage())
	if err == nil {
		t.Fatal("Write reported success for a send whose outcome is unknown")
	}
	if len(store.released) != 0 {
		t.Fatalf("the claim was released after an unknown outcome: Hermod's RetrySink "+
			"would then send the same mail again, to a recipient who may already have it (released=%v)",
			store.released)
	}
	if store.heldKeys() != 1 {
		t.Errorf("%d keys claimed, want the one that guards the retry", store.heldKeys())
	}
	if !strings.Contains(err.Error(), "not known") {
		t.Errorf("error %q does not say the outcome is unknown, which is the reason it is not retried", err)
	}
}

func TestWrite_UnknownOutcomeWithoutAStoreSaysSo(t *testing.T) {
	srv, _ := gateway(t, func(http.ResponseWriter, []byte) {
		panic(http.ErrAbortHandler)
	})
	srv.Config.ErrorLog = nil

	sink, err := New(baseConfig(srv.URL), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	err = sink.Write(context.Background(), testMessage())
	if err == nil {
		t.Fatal("Write reported success for a send whose outcome is unknown")
	}
	// With no store there is nothing to dedup on, so the retry that follows may
	// deliver twice. The error has to say that rather than look routine.
	if !strings.Contains(err.Error(), "idempotency") {
		t.Errorf("error %q does not point at the setting that would prevent a duplicate", err)
	}
}

func TestWrite_SuppressesADuplicate(t *testing.T) {
	var sends int
	var mu sync.Mutex
	srv, _ := gateway(t, func(w http.ResponseWriter, _ []byte) {
		mu.Lock()
		sends++
		mu.Unlock()
		accepted(w, nil)
	})

	sink, err := New(baseConfig(srv.URL), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	sink.EnableIdempotency(true)
	sink.SetIdempotencyStore(newMemStore())

	for range 2 {
		if err := sink.Write(context.Background(), testMessage()); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if sends != 1 {
		t.Errorf("the gateway saw %d sends of the same message, want 1", sends)
	}
	if dedup, _ := sink.LastWriteIdempotent(); !dedup {
		t.Error("the second write was not reported as a deduplicated one")
	}
}

// --- batching ----------------------------------------------------------------

func TestWriteBatch_SendsEachMessage(t *testing.T) {
	srv, received := gateway(t, accepted)
	sink, err := New(baseConfig(srv.URL), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	msgs := []hermod.Message{
		&mockMessage{id: "a", data: map[string]any{"email": "a@example.org", "name": "A", "total": "1"}},
		&mockMessage{id: "b", data: map[string]any{"email": "b@example.org", "name": "B", "total": "2"}},
	}
	if err := sink.WriteBatch(context.Background(), msgs); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}
	if len(*received) != 2 {
		t.Fatalf("gateway saw %d sends, want 2 — mail has no batch endpoint", len(*received))
	}
}

// --- templated routing fields ------------------------------------------------
//
// ProviderID and TemplateID are per-message fields on the SDK's Message, so a
// workflow can pick the sending provider or the stored template from the row:
// "welcome" mail and "receipt" mail off one stream, without one sink each.

func TestWrite_RendersTheProviderID(t *testing.T) {
	srv, received := gateway(t, accepted)

	cfg := baseConfig(srv.URL)
	cfg.ProviderID = "{{.provider}}"
	sink, err := New(cfg, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	msg := testMessage()
	msg.data["provider"] = "prov-7"
	if err := sink.Write(context.Background(), msg); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if got, _ := (*received)[0]["providerId"].(string); got != "prov-7" {
		t.Errorf("providerId = %q, want prov-7 rendered from the message", got)
	}
}

func TestWrite_RendersTheTemplateID(t *testing.T) {
	srv, received := gateway(t, accepted)

	cfg := baseConfig(srv.URL)
	cfg.TemplateID = "{{.kind}}-v2"
	sink, err := New(cfg, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	msg := testMessage()
	msg.data["kind"] = "welcome"
	if err := sink.Write(context.Background(), msg); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if got, _ := (*received)[0]["templateId"].(string); got != "welcome-v2" {
		t.Errorf("templateId = %q, want welcome-v2 rendered from the message", got)
	}
}

// A template over a field the message does not have renders "<no value>".
// Sending that is a send from the wrong provider, or against a template the
// gateway does not have — both are worse than a refusal.
func TestWrite_RefusesARoutingFieldThatRendersEmpty(t *testing.T) {
	for _, tc := range []struct {
		name  string
		mutex func(*Config)
		want  string
	}{
		{"provider id", func(c *Config) { c.ProviderID = "{{.absent}}" }, "provider id"},
		{"template id", func(c *Config) { c.TemplateID = "{{.absent}}" }, "template id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, received := gateway(t, accepted)

			cfg := baseConfig(srv.URL)
			tc.mutex(&cfg)
			sink, err := New(cfg, nil)
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			err = sink.Write(context.Background(), testMessage())
			if err == nil {
				t.Fatalf("Write succeeded with a %s that rendered empty", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not name the %s", err, tc.want)
			}
			if len(*received) != 0 {
				t.Errorf("gateway saw %d sends; a message that cannot be routed must not be sent", len(*received))
			}
		})
	}
}

// A template id that renders empty must not fall through to the body fallback:
// that would quietly mail the formatter's output instead of the template.
func TestWrite_ATemplateIDThatRendersEmptyDoesNotFallBackToTheBody(t *testing.T) {
	srv, received := gateway(t, accepted)

	cfg := baseConfig(srv.URL)
	cfg.HTML = ""
	cfg.TemplateID = "{{.absent}}"
	sink, err := New(cfg, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := sink.Write(context.Background(), testMessage()); err == nil {
		t.Fatal("Write succeeded with a template id that rendered empty")
	}
	if len(*received) != 0 {
		t.Errorf("gateway saw %d sends, want none", len(*received))
	}
}

// --- templated gateway and key -----------------------------------------------
//
// These two decide where a tenant-wide credential is sent, so templating them
// is only allowed against an explicit allowlist. See Config.AllowedHosts.

func TestNew_TemplatedGatewayURLRequiresAnAllowlist(t *testing.T) {
	cfg := baseConfig("{{.gateway}}")
	if _, err := New(cfg, nil); err == nil {
		t.Fatal("New accepted a templated gateway url with no allowlist, so a row could send the api key anywhere")
	}
}

func TestNew_TemplatedAPIKeyRequiresAnAllowlist(t *testing.T) {
	cfg := baseConfig("https://mail.example.com")
	cfg.APIKey = "{{.key}}"
	if _, err := New(cfg, nil); err == nil {
		t.Fatal("New accepted a templated api key with no allowlist")
	}
}

func TestWrite_RendersTheGatewayURL(t *testing.T) {
	first, firstSeen := gateway(t, accepted)
	second, secondSeen := gateway(t, accepted)

	cfg := baseConfig("{{.gateway}}")
	cfg.AllowedHosts = []string{"127.0.0.1"}
	sink, err := New(cfg, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for _, url := range []string{first.URL, second.URL} {
		msg := testMessage()
		msg.data["gateway"] = url
		if err := sink.Write(context.Background(), msg); err != nil {
			t.Fatalf("Write to %s: %v", url, err)
		}
	}

	if len(*firstSeen) != 1 || len(*secondSeen) != 1 {
		t.Fatalf("sends landed %d/%d, want one at each gateway the messages named",
			len(*firstSeen), len(*secondSeen))
	}
}

func TestWrite_ReusesOneClientPerGateway(t *testing.T) {
	srv, _ := gateway(t, accepted)

	cfg := baseConfig("{{.gateway}}")
	cfg.AllowedHosts = []string{"127.0.0.1"}
	sink, err := New(cfg, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for range 3 {
		msg := testMessage()
		msg.data["gateway"] = srv.URL
		if err := sink.Write(context.Background(), msg); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	sink.mu.Lock()
	n := len(sink.clients)
	sink.mu.Unlock()
	if n != 1 {
		t.Errorf("cached %d clients for one gateway, want 1 — a client per message leaks a connection pool", n)
	}
}

func TestWrite_RefusesAGatewayOffTheAllowlist(t *testing.T) {
	cfg := baseConfig("{{.gateway}}")
	cfg.APIKey = "{{.key}}"
	cfg.AllowedHosts = []string{"mail.example.com"}
	sink, err := New(cfg, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	msg := testMessage()
	msg.data["gateway"] = "https://attacker.example.net"
	msg.data["key"] = "secret-key-xyz"

	err = sink.Write(context.Background(), msg)
	if err == nil {
		t.Fatal("Write sent the api key to a host that is not on the allowlist")
	}
	if !strings.Contains(err.Error(), "attacker.example.net") {
		t.Errorf("error %q does not name the host it refused", err)
	}
	if strings.Contains(err.Error(), "secret-key-xyz") {
		t.Errorf("error leaks the api key: %q", err)
	}
}

func TestWrite_AllowlistAcceptsAWildcardSubdomain(t *testing.T) {
	cfg := baseConfig("{{.gateway}}")
	cfg.AllowedHosts = []string{"*.example.com"}
	// These hosts do not resolve; an allowed one fails in the transport instead,
	// and the timeout is what stops that taking the DNS resolver's own patience.
	cfg.Timeout = 2 * time.Second
	sink, err := New(cfg, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for _, tc := range []struct {
		host string
		ok   bool
	}{
		{"https://acme.example.com", true},
		{"https://eu.acme.example.com", true},
		{"https://example.com", false},
		{"https://notexample.com", false},
		{"https://example.com.attacker.net", false},
	} {
		msg := testMessage()
		msg.data["gateway"] = tc.host
		err := sink.Write(context.Background(), msg)
		refused := err != nil && strings.Contains(err.Error(), "not on the allowlist")
		if tc.ok && refused {
			t.Errorf("%s was refused by *.example.com", tc.host)
		}
		if !tc.ok && !refused {
			t.Errorf("%s was accepted by *.example.com (err=%v)", tc.host, err)
		}
	}
}

func TestWrite_RendersTheAPIKey(t *testing.T) {
	var seen []string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Get("X-API-Key"))
		mu.Unlock()
		accepted(w, nil)
	}))
	t.Cleanup(srv.Close)

	cfg := baseConfig(srv.URL)
	cfg.APIKey = "{{.key}}"
	cfg.AllowedHosts = []string{"127.0.0.1"}
	sink, err := New(cfg, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	msg := testMessage()
	msg.data["key"] = "tenant-key-9"
	if err := sink.Write(context.Background(), msg); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if len(seen) != 1 || seen[0] != "tenant-key-9" {
		t.Errorf("gateway saw keys %v, want [tenant-key-9] rendered from the message", seen)
	}
}

func TestWrite_RefusesAnAPIKeyThatRendersEmpty(t *testing.T) {
	srv, received := gateway(t, accepted)

	cfg := baseConfig(srv.URL)
	cfg.APIKey = "{{.absent}}"
	cfg.AllowedHosts = []string{"127.0.0.1"}
	sink, err := New(cfg, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	err = sink.Write(context.Background(), testMessage())
	if err == nil {
		t.Fatal("Write succeeded with an api key that rendered empty")
	}
	if !strings.Contains(err.Error(), "api key") {
		t.Errorf("error %q does not name the api key", err)
	}
	if len(*received) != 0 {
		t.Errorf("gateway saw %d sends, want none", len(*received))
	}
}

// Two gateways are two destinations: the same mail sent to each is two sends,
// and suppressing the second would silently drop it.
func TestIdempotencyKey_ATemplatedGatewayIsPartOfTheIdentity(t *testing.T) {
	cfg := baseConfig("{{.gateway}}")
	cfg.AllowedHosts = []string{"*.example.com"}
	sink, err := New(cfg, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	key := func(gateway string) string {
		t.Helper()
		msg := testMessage()
		msg.data["gateway"] = "https://" + gateway
		mail, err := sink.compose(msg)
		if err != nil {
			t.Fatalf("compose: %v", err)
		}
		k, err := sink.idempotencyKey(msg, mail)
		if err != nil {
			t.Fatalf("idempotencyKey: %v", err)
		}
		return k
	}

	if key("one.example.com") == key("two.example.com") {
		t.Error("the same mail to two gateways hashed to one key, so the second send would be suppressed")
	}
}

// A static gateway must keep hashing to the key it always did: changing the
// derivation would orphan every claim already in the store, and a message whose
// claim went missing is a message that gets mailed a second time.
func TestIdempotencyKey_AStaticGatewayIsNotPartOfTheIdentity(t *testing.T) {
	one, err := New(baseConfig("https://one.example.com"), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	two, err := New(baseConfig("https://two.example.com"), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	keyOf := func(s *Sink) string {
		t.Helper()
		msg := testMessage()
		mail, err := s.compose(msg)
		if err != nil {
			t.Fatalf("compose: %v", err)
		}
		k, err := s.idempotencyKey(msg, mail)
		if err != nil {
			t.Fatalf("idempotencyKey: %v", err)
		}
		return k
	}

	if keyOf(one) != keyOf(two) {
		t.Error("a static gateway url changed the derived key; existing claims would be orphaned")
	}
}

// A wildcard allowlist admits unlimited hostnames and says nothing at all about
// api keys, so both halves of the cache key come from the row. Without a bound
// this is a map an upstream table grows without limit.
func TestWrite_BoundsTheClientCache(t *testing.T) {
	cfg := baseConfig("{{.gateway}}")
	cfg.APIKey = "{{.key}}"
	cfg.AllowedHosts = []string{"*.mail.invalid"}
	// .invalid never resolves (RFC 6761), so each send fails in the transport
	// without waiting on a real network. The client is cached before the send,
	// which is the part under test.
	cfg.Timeout = time.Second
	sink, err := New(cfg, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for i := range maxCachedClients * 3 {
		msg := testMessage()
		msg.data["gateway"] = fmt.Sprintf("https://h%d.mail.invalid", i)
		msg.data["key"] = fmt.Sprintf("key-%d", i)
		_ = sink.Write(context.Background(), msg)
	}

	sink.mu.Lock()
	n := len(sink.clients)
	sink.mu.Unlock()
	if n > maxCachedClients {
		t.Errorf("cached %d clients, want at most %d — a row-supplied key with no bound is a leak",
			n, maxCachedClients)
	}
}

// Eviction has to be least-recently-used, not arbitrary: a flood of one-off
// gateways must not cost the busy one its client on every message.
func TestWrite_KeepsTheGatewayItKeepsUsing(t *testing.T) {
	srv, _ := gateway(t, accepted)

	cfg := baseConfig("{{.gateway}}")
	cfg.AllowedHosts = []string{"127.0.0.1", "*.mail.invalid"}
	cfg.Timeout = time.Second
	sink, err := New(cfg, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	hot := func() {
		msg := testMessage()
		msg.data["gateway"] = srv.URL
		if err := sink.Write(context.Background(), msg); err != nil {
			t.Fatalf("Write to the hot gateway: %v", err)
		}
	}

	hot()
	for i := range maxCachedClients * 2 {
		msg := testMessage()
		msg.data["gateway"] = fmt.Sprintf("https://h%d.mail.invalid", i)
		_ = sink.Write(context.Background(), msg)
		hot()
	}

	sink.mu.Lock()
	_, kept := sink.clients[clientKey(srv.URL, "key-123")]
	sink.mu.Unlock()
	if !kept {
		t.Error("the gateway used on every message was evicted by one-off gateways")
	}
}
