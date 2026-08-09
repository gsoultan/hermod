package registry

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/user/hermod/pkg/comm/message"

	_ "github.com/user/hermod/pkg/comm/transformer/lookup"
)

// ---------------------------------------------------------------------------
// api_lookup.
//
// The last of the eleven transformation types the retired browser specs
// covered to have no Go test. It enriches a message from an HTTP endpoint, so
// httptest is enough — no build tag, no external service.
//
// The failure worth guarding is not a broken request but a silent one: a lookup
// that cannot enrich must not hand the sink a message that looks enriched.
// ---------------------------------------------------------------------------

func TestAPILookupEnrichesFromTheEndpoint(t *testing.T) {
	reg := newSimRegistry(t)

	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"customer": map[string]any{"tier": "gold", "region": "emea"},
		})
	}))
	t.Cleanup(srv.Close)

	got := transform(t, reg,
		map[string]any{"customer_code": "C-1"},
		map[string]any{
			"transType":    "api_lookup",
			"method":       "GET",
			"url":          srv.URL + "/customers/C-1",
			"responsePath": "customer.tier",
			"targetField":  "tier",
		})

	if gotPath != "/customers/C-1" {
		t.Errorf("the endpoint saw path %q, want /customers/C-1", gotPath)
	}
	if got["tier"] != "gold" {
		t.Errorf("tier = %v, want gold: the response path was not applied, so the "+
			"message reaches the sink without the field the workflow asked for", got["tier"])
	}
	// The original field must survive an enrichment.
	if got["customer_code"] != "C-1" {
		t.Errorf("customer_code = %v after enrichment, want it unchanged", got["customer_code"])
	}
}

// TestAPILookupDoesNotSilentlySucceedOnAnError is the property that matters.
//
// A lookup against an endpoint that is down, or that answers with something the
// response path cannot find, must not produce a message that looks enriched.
// Whether it fails or writes a documented default, what it must not do is put
// the message through unchanged while reporting success — the sink would then
// store a record missing the field the pipeline exists to add.
func TestAPILookupDoesNotSilentlySucceedOnAnError(t *testing.T) {
	reg := newSimRegistry(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "upstream exploded", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetAfter([]byte(`{"customer_code":"C-1"}`))
	msg.SetPayload([]byte(`{"customer_code":"C-1"}`))

	out, err := reg.applyTransformation(t.Context(), msg, "api_lookup", map[string]any{
		"transType":    "api_lookup",
		"method":       "GET",
		"url":          srv.URL + "/customers/C-1",
		"responsePath": "customer.tier",
		"targetField":  "tier",
		"maxRetries":   "0",
	})

	if err != nil {
		return // Reported the failure; that is the behaviour we want.
	}
	if out == nil {
		return // Dropped deliberately; also explicit.
	}
	t.Cleanup(func() {
		if out != msg {
			out.Release()
		}
	})

	var got map[string]any
	if raw := out.After(); len(raw) > 0 {
		_ = json.Unmarshal(raw, &got)
	}
	if _, enriched := got["tier"]; !enriched {
		t.Error("a failed lookup returned success with the target field absent; the sink " +
			"stores a record missing the field the pipeline exists to add, and nothing " +
			"anywhere reports it")
	}
}
