package lookup

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/comm/transformer"
)

// panmailGateway answers ListEmailProviders with the given providers and
// records what it was asked.
type panmailGateway struct {
	srv    *httptest.Server
	calls  atomic.Int32
	apiKey atomic.Value
	body   atomic.Value
}

func newPanmailGateway(t *testing.T, status int, reply string) *panmailGateway {
	t.Helper()
	g := &panmailGateway{}
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.calls.Add(1)
		if r.URL.Path != "/panmail.v1.EmailProviderService/ListEmailProviders" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		g.apiKey.Store(r.Header.Get("X-API-Key"))
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		g.body.Store(body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(g.srv.Close)
	return g
}

const twoProviders = `{"providers":[
  {"id":"p1","name":"Primary","type":"PROVIDER_TYPE_SMTP","allowedDomains":["example.com"],
   "smtp":{"host":"smtp.example.com","username":"u"}},
  {"id":"p2","name":"Bulk","type":"PROVIDER_TYPE_SES"}
]}`

func runPanmailProviders(t *testing.T, tr *PanmailProvidersTransformer, reg *cachingFakeRegistry,
	cfg map[string]any, fields map[string]any) (map[string]any, error) {
	t.Helper()
	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	for k, v := range fields {
		msg.SetData(k, v)
	}
	ctx := context.WithValue(t.Context(), hermod.RegistryKey, reg)
	out, err := tr.Transform(ctx, msg, cfg)
	if out == nil {
		return nil, err
	}
	return out.Data(), err
}

func newPanmailProvidersFixture() (*PanmailProvidersTransformer, *cachingFakeRegistry) {
	return &PanmailProvidersTransformer{sf: newSingleflightGroup()}, &cachingFakeRegistry{cache: map[string]any{}}
}

func TestPanmailProvidersIsRegistered(t *testing.T) {
	if _, ok := transformer.Get("panmail_providers"); !ok {
		t.Fatal("panmail_providers is not registered")
	}
}

func TestPanmailProvidersWritesTheListToTheTargetField(t *testing.T) {
	g := newPanmailGateway(t, http.StatusOK, twoProviders)
	tr, reg := newPanmailProvidersFixture()

	got, err := runPanmailProviders(t, tr, reg, map[string]any{
		"baseUrl":     g.srv.URL,
		"apiKey":      "key-1",
		"targetField": "providers",
	}, nil)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}

	list, ok := got["providers"].([]map[string]any)
	if !ok || len(list) != 2 {
		t.Fatalf("providers = %#v", got["providers"])
	}
	first := list[0]
	if first["id"] != "p1" || first["name"] != "Primary" || first["type"] != "smtp" {
		t.Fatalf("first = %#v", first)
	}
	if d, _ := first["allowedDomains"].([]string); len(d) != 1 || d[0] != "example.com" {
		t.Fatalf("allowedDomains = %#v", first["allowedDomains"])
	}
	if list[1]["type"] != "ses" {
		t.Fatalf("second type = %#v", list[1]["type"])
	}
	// Connection settings never reach the message.
	if _, ok := first["smtp"]; ok {
		t.Fatalf("connection settings leaked into the message: %#v", first)
	}
	if g.apiKey.Load() != "key-1" {
		t.Fatalf("X-API-Key = %v", g.apiKey.Load())
	}
}

func TestPanmailProvidersDefaultsTheTargetField(t *testing.T) {
	g := newPanmailGateway(t, http.StatusOK, twoProviders)
	tr, reg := newPanmailProvidersFixture()

	got, err := runPanmailProviders(t, tr, reg, map[string]any{"baseUrl": g.srv.URL, "apiKey": "k"}, nil)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if _, ok := got["panmail_providers"]; !ok {
		t.Fatalf("no panmail_providers field: %#v", got)
	}
}

func TestPanmailProvidersSendsTheFilters(t *testing.T) {
	g := newPanmailGateway(t, http.StatusOK, `{"providers":[{"id":"p2","name":"Bulk","type":"PROVIDER_TYPE_SES"}]}`)
	tr, reg := newPanmailProvidersFixture()

	_, err := runPanmailProviders(t, tr, reg, map[string]any{
		"baseUrl":      g.srv.URL,
		"apiKey":       "k",
		"name":         "{{.region}}",
		"providerType": "ses",
	}, map[string]any{"region": "eu"})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	body, _ := g.body.Load().(map[string]any)
	if body["name"] != "eu" || body["type"] != "PROVIDER_TYPE_SES" {
		t.Fatalf("request body = %#v", body)
	}
}

func TestPanmailProvidersRefusesAnUnknownProviderType(t *testing.T) {
	g := newPanmailGateway(t, http.StatusOK, twoProviders)
	tr, reg := newPanmailProvidersFixture()

	_, err := runPanmailProviders(t, tr, reg, map[string]any{
		"baseUrl": g.srv.URL, "apiKey": "k", "providerType": "pigeon",
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "pigeon") {
		t.Fatalf("err = %v, want it to name the bad type", err)
	}
	if g.calls.Load() != 0 {
		t.Fatalf("the gateway was called %d times", g.calls.Load())
	}
}

func TestPanmailProvidersRequiresGatewayAndKey(t *testing.T) {
	tr, reg := newPanmailProvidersFixture()
	for name, cfg := range map[string]map[string]any{
		"no gateway": {"apiKey": "k"},
		"no key":     {"baseUrl": "https://mail.example.com"},
	} {
		if _, err := runPanmailProviders(t, tr, reg, cfg, nil); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

// The gateway and key choose where a tenant credential is sent. Row data must
// not be able to pick either, so they resolve env and secret functions only.
func TestPanmailProvidersDoesNotLetRowDataPickTheGatewayOrKey(t *testing.T) {
	g := newPanmailGateway(t, http.StatusOK, twoProviders)
	tr, reg := newPanmailProvidersFixture()

	_, err := runPanmailProviders(t, tr, reg, map[string]any{
		"baseUrl": "{{.gateway}}",
		"apiKey":  "k",
	}, map[string]any{"gateway": g.srv.URL})
	if err == nil {
		t.Fatal("a gateway taken from row data was accepted")
	}
	if g.calls.Load() != 0 {
		t.Fatalf("the row-chosen gateway was called %d times", g.calls.Load())
	}
}

func TestPanmailProvidersReadsTheKeyFromASecret(t *testing.T) {
	t.Setenv("HERMOD_SECRET_PANMAIL_TEST_KEY", "from-secret")
	g := newPanmailGateway(t, http.StatusOK, twoProviders)
	tr, reg := newPanmailProvidersFixture()

	_, err := runPanmailProviders(t, tr, reg, map[string]any{
		"baseUrl": g.srv.URL,
		"apiKey":  `{{secret("PANMAIL_TEST_KEY")}}`,
	}, nil)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if g.apiKey.Load() != "from-secret" {
		t.Fatalf("X-API-Key = %v, want from-secret", g.apiKey.Load())
	}
}

func TestPanmailProvidersCachesTheList(t *testing.T) {
	g := newPanmailGateway(t, http.StatusOK, twoProviders)
	tr, reg := newPanmailProvidersFixture()
	cfg := map[string]any{"baseUrl": g.srv.URL, "apiKey": "k"}

	for range 3 {
		if _, err := runPanmailProviders(t, tr, reg, cfg, nil); err != nil {
			t.Fatalf("Transform: %v", err)
		}
	}
	if g.calls.Load() != 1 {
		t.Fatalf("gateway calls = %d, want 1", g.calls.Load())
	}
}

func TestPanmailProvidersCacheIsPerKey(t *testing.T) {
	g := newPanmailGateway(t, http.StatusOK, twoProviders)
	tr, reg := newPanmailProvidersFixture()

	for _, key := range []string{"tenant-a", "tenant-b"} {
		if _, err := runPanmailProviders(t, tr, reg, map[string]any{"baseUrl": g.srv.URL, "apiKey": key}, nil); err != nil {
			t.Fatalf("Transform: %v", err)
		}
	}
	if g.calls.Load() != 2 {
		t.Fatalf("gateway calls = %d, want 2 (one per api key)", g.calls.Load())
	}
	for k := range reg.cache {
		if strings.Contains(k, "tenant-a") || strings.Contains(k, "tenant-b") {
			t.Fatalf("api key written into the cache key in the clear: %q", k)
		}
	}
}

func TestPanmailProvidersTTLZeroDisablesTheCache(t *testing.T) {
	g := newPanmailGateway(t, http.StatusOK, twoProviders)
	tr, reg := newPanmailProvidersFixture()
	cfg := map[string]any{"baseUrl": g.srv.URL, "apiKey": "k", "ttl": "0"}

	for range 2 {
		if _, err := runPanmailProviders(t, tr, reg, cfg, nil); err != nil {
			t.Fatalf("Transform: %v", err)
		}
	}
	if g.calls.Load() != 2 {
		t.Fatalf("gateway calls = %d, want 2", g.calls.Load())
	}
}

func TestPanmailProvidersReportsARefusal(t *testing.T) {
	g := newPanmailGateway(t, http.StatusForbidden, `{"code":"permission_denied","message":"api key lacks providers:read"}`)
	tr, reg := newPanmailProvidersFixture()

	_, err := runPanmailProviders(t, tr, reg, map[string]any{"baseUrl": g.srv.URL, "apiKey": "k"}, nil)
	if err == nil || !strings.Contains(err.Error(), "providers:read") {
		t.Fatalf("err = %v, want the gateway's reason", err)
	}
	if strings.Contains(err.Error(), `"k"`) {
		t.Fatalf("the api key reached the error: %v", err)
	}
}
