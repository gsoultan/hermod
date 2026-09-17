package lookup

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"golang.org/x/sync/singleflight"
)

// ---------------------------------------------------------------------------
// api_lookup's cache key.
//
// Unlike db_lookup it resolves the URL and the body before keying on them, so
// the "every message gets message one's answer" collapse does not happen for
// those two. Everything else a message can vary does not reach the key:
// headers, the auth credential, and responsePath are all applied after it is
// built. Two messages that differ only there share one entry -- and because the
// key is also the singleflight key, a concurrent pair shares one HTTP request
// as well, so the second caller is answered with the first caller's response
// even on a cold cache.
// ---------------------------------------------------------------------------

// echoServer answers with whatever identity the caller presented, so a response
// that belongs to a different caller is visible in the result rather than
// having to be inferred from a request count.
func echoServer(t *testing.T, field string, read func(*http.Request) string) (*httptest.Server, func() int) {
	t.Helper()
	var mu sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{field: read(r)})
	}))
	t.Cleanup(srv.Close)
	return srv, func() int {
		mu.Lock()
		defer mu.Unlock()
		return calls
	}
}

func apiLookup(t *testing.T, tr *APILookupTransformer, reg *cachingFakeRegistry, cfg map[string]any, fields map[string]any) map[string]any {
	t.Helper()

	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	for k, v := range fields {
		msg.SetData(k, v)
	}

	ctx := context.WithValue(t.Context(), hermod.RegistryKey, reg)
	out, err := tr.Transform(ctx, msg, cfg)
	if err != nil {
		t.Fatalf("Transform(%v): %v", fields, err)
	}
	if out == nil {
		t.Fatalf("Transform(%v) returned no message", fields)
	}
	return out.Data()
}

func newAPIFixture() (*APILookupTransformer, *cachingFakeRegistry) {
	return &APILookupTransformer{sf: newSingleflightGroup()}, &cachingFakeRegistry{cache: map[string]any{}}
}

// A templated header is per-message input that selects the response, so it
// belongs in the key. The shape is ordinary: one endpoint, the tenant in a
// header.
func TestAPILookupCacheKeyIncludesTemplatedHeaders(t *testing.T) {
	srv, calls := echoServer(t, "tenant", func(r *http.Request) string {
		return r.Header.Get("X-Tenant-Id")
	})
	tr, reg := newAPIFixture()

	cfg := map[string]any{
		"method":       "GET",
		"url":          srv.URL + "/me",
		"headers":      `{"X-Tenant-Id":"{{.tenant}}"}`,
		"responsePath": "tenant",
		"targetField":  "who",
	}

	if got := apiLookup(t, tr, reg, cfg, map[string]any{"tenant": "acme"})["who"]; got != "acme" {
		t.Fatalf("first message: who = %#v, want %q", got, "acme")
	}

	second := apiLookup(t, tr, reg, cfg, map[string]any{"tenant": "globex"})["who"]
	if second != "globex" {
		t.Errorf("second message: who = %#v, want %q -- the cache key omits the resolved headers, "+
			"so a message from a different tenant is served the first tenant's response (calls=%d, cache=%v)",
			second, "globex", calls(), reg.cache)
	}
}

// The same defect on the credential. This one is not just wrong, it is a
// disclosure: the response belongs to whoever the first token identified.
func TestAPILookupCacheKeyIncludesTemplatedBearerToken(t *testing.T) {
	srv, calls := echoServer(t, "subject", func(r *http.Request) string {
		return r.Header.Get("Authorization")
	})
	tr, reg := newAPIFixture()

	cfg := map[string]any{
		"method":       "GET",
		"url":          srv.URL + "/profile",
		"authType":     "bearer",
		"token":        "{{.userToken}}",
		"responsePath": "subject",
		"targetField":  "profile",
	}

	first := apiLookup(t, tr, reg, cfg, map[string]any{"userToken": "token-alice"})["profile"]
	if first != "Bearer token-alice" {
		t.Fatalf("first message: profile = %#v, want %q", first, "Bearer token-alice")
	}

	second := apiLookup(t, tr, reg, cfg, map[string]any{"userToken": "token-bob"})["profile"]
	if second != "Bearer token-bob" {
		t.Errorf("second message: profile = %#v, want %q -- the cache key omits the resolved credential, "+
			"so Bob's message is enriched with the resource fetched using Alice's token (calls=%d)",
			second, "Bearer token-bob", calls())
	}
}

// Basic auth takes the same path and needs the same treatment.
func TestAPILookupCacheKeyIncludesTemplatedBasicAuth(t *testing.T) {
	srv, _ := echoServer(t, "subject", func(r *http.Request) string {
		u, _, _ := r.BasicAuth()
		return u
	})
	tr, reg := newAPIFixture()

	cfg := map[string]any{
		"method":       "GET",
		"url":          srv.URL + "/profile",
		"authType":     "basic",
		"username":     "{{.user}}",
		"password":     "{{.pass}}",
		"responsePath": "subject",
		"targetField":  "profile",
	}

	first := apiLookup(t, tr, reg, cfg, map[string]any{"user": "alice", "pass": "a"})["profile"]
	if first != "alice" {
		t.Fatalf("first message: profile = %#v, want %q", first, "alice")
	}

	second := apiLookup(t, tr, reg, cfg, map[string]any{"user": "bob", "pass": "b"})["profile"]
	if second != "bob" {
		t.Errorf("second message: profile = %#v, want %q -- the cache key omits basic-auth credentials",
			second, "bob")
	}
}

// responsePath is applied inside the cached closure, so the stored value is
// already extracted. Two nodes pointed at one endpoint asking for different
// fields therefore collide: the second is handed the first's sub-value. The key
// has no node or config discriminator at all.
func TestAPILookupCacheKeyIncludesResponsePath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"name":"Ada","email":"ada@example.com"}`)
	}))
	t.Cleanup(srv.Close)

	tr, reg := newAPIFixture()
	base := map[string]any{"method": "GET", "url": srv.URL + "/user/1"}

	nameCfg := map[string]any{"responsePath": "name", "targetField": "n"}
	emailCfg := map[string]any{"responsePath": "email", "targetField": "e"}
	for k, v := range base {
		nameCfg[k] = v
		emailCfg[k] = v
	}

	if got := apiLookup(t, tr, reg, nameCfg, nil)["n"]; got != "Ada" {
		t.Fatalf("name node: n = %#v, want %q", got, "Ada")
	}
	if got := apiLookup(t, tr, reg, emailCfg, nil)["e"]; got != "ada@example.com" {
		t.Errorf("email node: e = %#v, want %q -- the cache key omits responsePath, so a second node "+
			"reading a different field from the same endpoint gets the first node's extraction",
			got, "ada@example.com")
	}
}

// The counterpart: two messages that really are identical must still share one
// entry, or the fix has simply disabled the cache.
func TestAPILookupStillCachesIdenticalRequests(t *testing.T) {
	srv, calls := echoServer(t, "tenant", func(r *http.Request) string {
		return r.Header.Get("X-Tenant-Id")
	})
	tr, reg := newAPIFixture()

	cfg := map[string]any{
		"method":       "GET",
		"url":          srv.URL + "/me",
		"headers":      `{"X-Tenant-Id":"{{.tenant}}"}`,
		"responsePath": "tenant",
		"targetField":  "who",
	}

	for range 3 {
		if got := apiLookup(t, tr, reg, cfg, map[string]any{"tenant": "acme"})["who"]; got != "acme" {
			t.Fatalf("who = %#v, want %q", got, "acme")
		}
	}
	if calls() != 1 {
		t.Errorf("the endpoint was called %d times for 3 identical lookups; want 1", calls())
	}
}

func newSingleflightGroup() *singleflight.Group { return &singleflight.Group{} }
