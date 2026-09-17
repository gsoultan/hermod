package lookup

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// ---------------------------------------------------------------------------
// The rest of api_lookup's silent failures.
//
// db_lookup was given an explicit miss policy, a bounded cache and a loud
// reaction to unusable configuration. api_lookup was not, so every one of these
// still ended in "the message reaches the sink looking enriched" or "the
// control in the editor does nothing".
// ---------------------------------------------------------------------------

// apiLookupErr runs one message and returns the data and the error, for the
// cases where the error is the point.
func apiLookupErr(t *testing.T, cfg map[string]any, fields map[string]any) (map[string]any, error) {
	t.Helper()

	tr, reg := newAPIFixture()
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

// ---- 1. onMiss ------------------------------------------------------------

// A 2xx response whose responsePath resolves to nothing is the exact case
// onMiss exists for: the call succeeded and enriched nothing. It returned the
// message unchanged with a nil error, so the sink could not tell it from a hit.
func TestAPILookupHonoursOnMissFailWhenTheResponsePathIsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"customer":{}}`))
	}))
	t.Cleanup(srv.Close)

	_, err := apiLookupErr(t, map[string]any{
		"method":       "GET",
		"url":          srv.URL + "/c/1",
		"responsePath": "customer.tier",
		"targetField":  "tier",
		"onMiss":       "fail",
	}, nil)

	if err == nil {
		t.Error("a 200 whose responsePath matched nothing returned no error under onMiss=fail; " +
			"the message reaches the sink indistinguishable from an enriched one")
	}
}

// The inferred policy has to keep behaving the way it does today: a
// defaultValue means the author wanted misses filled in.
func TestAPILookupWritesTheDefaultOnAnEmptyResponsePath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"customer":{}}`))
	}))
	t.Cleanup(srv.Close)

	got, err := apiLookupErr(t, map[string]any{
		"method":       "GET",
		"url":          srv.URL + "/c/1",
		"responsePath": "customer.tier",
		"targetField":  "tier",
		"defaultValue": "unknown",
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["tier"] != "unknown" {
		t.Errorf("tier = %#v, want %q", got["tier"], "unknown")
	}
}

// Configuration too incomplete to make a request is a miss too, and the loudest
// policy has to reach it.
func TestAPILookupHonoursOnMissFailForIncompleteConfig(t *testing.T) {
	_, err := apiLookupErr(t, map[string]any{
		"method":      "GET",
		"targetField": "tier",
		"onMiss":      "fail",
	}, nil)

	if err == nil {
		t.Error("an api_lookup with no url returned success under onMiss=fail")
	}
}

// onMiss=fail must also override a defaultValue on a transport failure: "fill
// in empty responses, but a 500 is still a failed message" is not expressible
// today, because the default silently swallows the error.
func TestAPILookupOnMissFailOverridesTheDefaultValueOnAnHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	_, err := apiLookupErr(t, map[string]any{
		"method":       "GET",
		"url":          srv.URL + "/c/1",
		"targetField":  "tier",
		"defaultValue": "unknown",
		"onMiss":       "fail",
		"maxRetries":   "0",
	}, nil)

	if err == nil {
		t.Error("a 500 with a defaultValue configured returned success under onMiss=fail")
	}
}

// ...and the existing behaviour it must not break: a defaultValue with no
// explicit policy still swallows the error, and no defaultValue still reports
// it.
func TestAPILookupErrorHandlingWithoutAnExplicitPolicyIsUnchanged(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	base := map[string]any{
		"method": "GET", "url": srv.URL + "/c/1",
		"targetField": "tier", "maxRetries": "0",
	}

	withDefault := map[string]any{"defaultValue": "unknown"}
	for k, v := range base {
		withDefault[k] = v
	}
	got, err := apiLookupErr(t, withDefault, nil)
	if err != nil {
		t.Errorf("a defaultValue no longer swallows an HTTP error: %v", err)
	}
	if got["tier"] != "unknown" {
		t.Errorf("tier = %#v, want %q", got["tier"], "unknown")
	}

	if _, err := apiLookupErr(t, base, nil); err == nil {
		t.Error("an HTTP error with no defaultValue no longer reports a failure")
	}
}

// ---- 2. TTL ---------------------------------------------------------------

// "5" is not a Go duration. The parse failed, the error was discarded, and the
// zero value it left behind means "never expires" to SetLookupCache -- so the
// field whose whole purpose is bounding staleness silently unbounded it.
func TestAPILookupRejectsAnUnparseableTTL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tier":"gold"}`))
	}))
	t.Cleanup(srv.Close)

	_, err := apiLookupErr(t, map[string]any{
		"method":       "GET",
		"url":          srv.URL + "/c/1",
		"responsePath": "tier",
		"targetField":  "tier",
		"ttl":          "5",
	}, nil)

	if err == nil {
		t.Error(`ttl "5" was accepted; the parse failed and left 0, which means "cache forever"`)
	} else if !strings.Contains(err.Error(), "ttl") {
		t.Errorf("the error does not name the field at fault: %v", err)
	}
}

// An unset TTL must not mean "keep this HTTP response for the lifetime of the
// process".
func TestAPILookupDoesNotCacheForeverByDefault(t *testing.T) {
	tr, reg := newAPIFixture()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tier":"gold"}`))
	}))
	t.Cleanup(srv.Close)

	cfg := map[string]any{
		"method": "GET", "url": srv.URL + "/c/1",
		"responsePath": "tier", "targetField": "tier",
	}
	apiLookup(t, tr, reg, cfg, nil)

	if reg.sets != 1 {
		t.Fatalf("the lookup cached %d times, want 1", reg.sets)
	}
	if reg.lastTTL <= 0 {
		t.Errorf("an unset ttl stored the response with ttl=%v, which SetLookupCache treats as "+
			"never expiring: an API response is the last thing that should be cached forever", reg.lastTTL)
	}
}

// And "0" has to mean what it says, which is currently inexpressible: there is
// no way to turn this cache off.
func TestAPILookupTTLZeroDisablesTheCache(t *testing.T) {
	tr, reg := newAPIFixture()
	var mu sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		_, _ = w.Write([]byte(`{"tier":"gold"}`))
	}))
	t.Cleanup(srv.Close)

	cfg := map[string]any{
		"method": "GET", "url": srv.URL + "/c/1",
		"responsePath": "tier", "targetField": "tier", "ttl": "0",
	}
	for range 3 {
		if got := apiLookup(t, tr, reg, cfg, nil)["tier"]; got != "gold" {
			t.Fatalf("tier = %#v, want gold", got)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if calls != 3 {
		t.Errorf("the endpoint was called %d times for 3 lookups with ttl=0; want 3 -- "+
			"ttl 0 must mean \"do not cache\", not \"cache forever\"", calls)
	}
}

// ---- 3. the HTTP client ---------------------------------------------------

// api_lookup reaches operator-configured endpoints, which in a self-hosted
// deployment are routinely on private addresses. httpclient.DefaultClient
// refuses those by design (SSRF protection, for URLs Hermod did not choose);
// DataClient is the one for this job, and it also brings the pooling that
// http.DefaultClient's zero-value transport does not.
func TestAPILookupReachesAPrivateAddress(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tier":"gold"}`))
	}))
	t.Cleanup(srv.Close)

	// httptest binds 127.0.0.1, so this fails outright on the SSRF-blocking
	// client and passes on either http.DefaultClient or DataClient.
	got, err := apiLookupErr(t, map[string]any{
		"method": "GET", "url": srv.URL + "/c/1",
		"responsePath": "tier", "targetField": "tier",
	}, nil)
	if err != nil {
		t.Fatalf("a loopback endpoint was refused: %v", err)
	}
	if got["tier"] != "gold" {
		t.Errorf("tier = %#v, want gold", got["tier"])
	}
}

// ---- 4. unusable JSON in the config --------------------------------------

// Malformed headers were dropped and the request went out without them -- as an
// unauthenticated call to a real endpoint, reported nowhere. A misconfiguration
// is not a miss, so this is an error regardless of onMiss, the same way
// db_lookup treats a CDC source.
func TestAPILookupReportsMalformedHeaderJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tier":"gold"}`))
	}))
	t.Cleanup(srv.Close)

	_, err := apiLookupErr(t, map[string]any{
		"method": "GET", "url": srv.URL + "/c/1",
		"headers":     `{"Authorization": "Bearer {{token}}"`, // no closing brace
		"targetField": "tier",
		"onMiss":      "passthrough",
	}, map[string]any{"token": "t"})

	if err == nil {
		t.Error("malformed headers JSON was dropped silently; the request went out unauthenticated")
	} else if !strings.Contains(err.Error(), "headers") {
		t.Errorf("the error does not name the field at fault: %v", err)
	}
}

func TestAPILookupReportsMalformedQueryParamJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tier":"gold"}`))
	}))
	t.Cleanup(srv.Close)

	_, err := apiLookupErr(t, map[string]any{
		"method": "GET", "url": srv.URL + "/c/1",
		"queryParams": `{"id": }`,
		"targetField": "tier",
	}, nil)

	if err == nil {
		t.Error("malformed queryParams JSON was dropped silently; the request went out unfiltered")
	} else if !strings.Contains(err.Error(), "queryParams") {
		t.Errorf("the error does not name the field at fault: %v", err)
	}
}

// ---- 5. Max Retries is a NumberInput -------------------------------------

// The editor's "Max Retries" is a NumberInput, so it saves a JSON number, and
// every reader here went through GetConfigString, which returns "" for a
// non-string. The control did nothing: a retry count set in the UI produced
// exactly one attempt.
func TestAPILookupRetriesWhenMaxRetriesIsANumber(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"tier":"gold"}`))
	}))
	t.Cleanup(srv.Close)

	got, err := apiLookupErr(t, map[string]any{
		"method": "GET", "url": srv.URL + "/c/1",
		"responsePath": "tier", "targetField": "tier",
		"maxRetries": float64(3), // what the editor's NumberInput round-trips as
		"retryDelay": "1ms",
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if calls != 3 {
		t.Errorf("the endpoint saw %d attempts, want 3 -- maxRetries was set as a number, "+
			"which GetConfigString reads as \"\", so the editor's retry control does nothing", calls)
	}
	if got["tier"] != "gold" {
		t.Errorf("tier = %#v, want gold", got["tier"])
	}
}
