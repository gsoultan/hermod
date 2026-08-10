package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Security headers.
//
// SECURITY.md claims a Content Security Policy is set, and until now nothing
// held that claim: the middleware could have been dropped from the chain, or
// the policy loosened to `default-src *`, and every test would still pass.
//
// A claimed control that nothing verifies is the same shape as the problems
// this codebase has been working through — documentation ahead of code — so
// these tests exist to make the claim checkable.
//
// They assert properties rather than the exact policy string. Pinning the
// literal would fail on every legitimate tweak and teach whoever hits it to
// update the expectation without reading it, which is how a test stops
// protecting anything.
// ---------------------------------------------------------------------------

func headersFor(t *testing.T, env map[string]string) http.Header {
	t.Helper()
	for k, v := range env {
		t.Setenv(k, v)
	}

	h := &Handler{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)

	h.SecurityHeadersMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)

	return rec.Result().Header
}

func TestSecurityHeadersAreSet(t *testing.T) {
	got := headersFor(t, nil)

	want := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
	}
	for header, expected := range want {
		if got.Get(header) != expected {
			t.Errorf("%s = %q, want %q", header, got.Get(header), expected)
		}
	}

	if got.Get("Content-Security-Policy") == "" {
		t.Error("no Content-Security-Policy header; SECURITY.md claims one is set")
	}
}

// TestDefaultCSPRestrictsSourcesToSelf: the point of the policy is that content
// only loads from origins we chose. A default-src of * or 'unsafe-eval'
// anywhere would leave XSS as good as unmitigated — which matters more than
// usual here, because an XSS is currently a session-compromise vector.
func TestDefaultCSPRestrictsSourcesToSelf(t *testing.T) {
	csp := headersFor(t, nil).Get("Content-Security-Policy")

	if !strings.Contains(csp, "default-src 'self'") {
		t.Errorf("default-src is not 'self': %s", csp)
	}
	for _, forbidden := range []string{"default-src *", "'unsafe-eval'", "script-src *"} {
		if strings.Contains(csp, forbidden) {
			t.Errorf("policy contains %q, which defeats it: %s", forbidden, csp)
		}
	}
}

// TestProductionCSPDropsInlineStyles pins the one difference between the
// environments, so a refactor cannot quietly make production as permissive as
// development.
func TestProductionCSPDropsInlineStyles(t *testing.T) {
	dev := headersFor(t, map[string]string{"HERMOD_ENV": "development"})
	prod := headersFor(t, map[string]string{"HERMOD_ENV": "production"})

	devCSP := dev.Get("Content-Security-Policy")
	prodCSP := prod.Get("Content-Security-Policy")

	if !strings.Contains(devCSP, "style-src 'self' 'unsafe-inline'") {
		t.Errorf("development policy no longer allows inline styles, which the UI relies on: %s", devCSP)
	}
	if strings.Contains(prodCSP, "'unsafe-inline'") {
		t.Errorf("production policy allows inline styles: %s", prodCSP)
	}
}

// TestCSPIsOverridable: a deployment fronted by a CDN or embedding the UI needs
// its own policy, and an un-overridable one is a reason to strip the middleware
// entirely — which is worse than a looser policy.
func TestCSPIsOverridable(t *testing.T) {
	custom := "default-src 'none'; script-src 'self'"
	got := headersFor(t, map[string]string{"HERMOD_CSP": custom})

	if got.Get("Content-Security-Policy") != custom {
		t.Errorf("HERMOD_CSP was ignored: got %q, want %q",
			got.Get("Content-Security-Policy"), custom)
	}
}

// TestCSPAllowsWebSockets guards a real regression: the UI's live views
// authenticate over WebSockets, and a connect-src without ws:/wss: blocks every
// one of them. The failure is silent — the socket simply never opens — so it is
// exactly the kind of thing to pin.
func TestCSPAllowsWebSockets(t *testing.T) {
	for _, env := range []map[string]string{nil, {"HERMOD_ENV": "production"}} {
		csp := headersFor(t, env).Get("Content-Security-Policy")
		if !strings.Contains(csp, "ws:") || !strings.Contains(csp, "wss:") {
			t.Errorf("connect-src does not permit WebSockets, so every live view breaks: %s", csp)
		}
	}
}
