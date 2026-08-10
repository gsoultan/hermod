package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// CSRF.
//
// The API authenticates with a cookie, and a browser attaches cookies to
// cross-site requests it was tricked into making. SameSite is the only thing
// currently standing between that and a state change — which is fine while it
// is Lax or Strict, and nothing at all the moment a deployment sets None to
// allow cross-origin embedding. SECURITY.md lists that as the prerequisite for
// SameSite=None, so this is that prerequisite.
//
// Double submit: the server issues a readable token in a cookie, the UI echoes
// it in a header, and the two must match. It works because an attacker on
// another origin can make the browser *send* the cookie but cannot read it to
// populate the header, and cannot set custom headers cross-origin at all.
//
// The rule that keeps this from breaking everything is who it applies to:
//
//   - cookie-authenticated state changes -> enforced. This is the vector.
//   - header-authenticated (Bearer, X-Worker-Token) -> exempt. An attacker
//     cannot set those headers cross-origin, so the request is not forgeable in
//     the first place, and enforcing would break every CLI, worker and
//     integration overnight.
//   - GET and HEAD -> exempt. They are read paths; a CSRF token on them buys
//     nothing and would break ordinary navigation.
// ---------------------------------------------------------------------------

func csrfRequest(t *testing.T, method, path, sessionTok, cookieTok, headerTok string) *http.Request {
	t.Helper()
	r := httptest.NewRequestWithContext(context.Background(), method, path, nil)
	if sessionTok != "" {
		r.AddCookie(&http.Cookie{Name: "hermod_session", Value: sessionTok})
	}
	if cookieTok != "" {
		r.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: cookieTok})
	}
	if headerTok != "" {
		r.Header.Set(CSRFHeaderName, headerTok)
	}
	return r
}

// TestCookieAuthStateChangeRequiresAMatchingToken is the protection itself.
func TestCookieAuthStateChangeRequiresAMatchingToken(t *testing.T) {
	withJWTConfig(t)
	session := mintSession(t, time.Hour)

	cases := []struct {
		name                 string
		cookieTok, headerTok string
		wantBlocked          bool
	}{
		{"no token at all", "", "", true},
		{"cookie but no header", "abc123", "", true},
		{"header but no cookie", "", "abc123", true},
		{"mismatched", "abc123", "different", true},
		{"matching", "abc123", "abc123", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := csrfRequest(t, http.MethodPost, "/api/workflows", session, tc.cookieTok, tc.headerTok)
			guarded(t, &Handler{}).ServeHTTP(rec, req)

			blocked := rec.Code == http.StatusForbidden
			if blocked != tc.wantBlocked {
				t.Errorf("got %d (blocked=%v), want blocked=%v", rec.Code, blocked, tc.wantBlocked)
			}
		})
	}
}

// TestReadsAreNotProtected: a CSRF token on a GET buys nothing and would break
// ordinary navigation.
func TestReadsAreNotProtected(t *testing.T) {
	withJWTConfig(t)
	session := mintSession(t, time.Hour)

	for _, method := range []string{http.MethodGet, http.MethodHead} {
		rec := httptest.NewRecorder()
		req := csrfRequest(t, method, "/api/workflows", session, "", "")
		guarded(t, &Handler{}).ServeHTTP(rec, req)

		if rec.Code == http.StatusForbidden {
			t.Errorf("%s /api/workflows was blocked for a missing CSRF token", method)
		}
	}
}

// TestHeaderAuthIsExempt is what keeps this from breaking every non-browser
// client. A request carrying Authorization is not forgeable cross-origin —
// the attacker cannot set that header — so requiring a second one is pure cost.
func TestHeaderAuthIsExempt(t *testing.T) {
	withJWTConfig(t)
	session := mintSession(t, time.Hour)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/workflows", nil)
	req.Header.Set("Authorization", "Bearer "+session)
	guarded(t, &Handler{}).ServeHTTP(rec, req)

	if rec.Code == http.StatusForbidden {
		t.Error("a Bearer-authenticated POST was blocked for a missing CSRF token; " +
			"this breaks hermodctl and every API integration for no security gain")
	}
}

// TestWorkerTokenIsExempt covers the data plane: worker agents post logs and
// heartbeats with X-Worker-Token, which an attacker equally cannot set.
func TestWorkerTokenIsExempt(t *testing.T) {
	withJWTConfig(t)
	t.Setenv("HERMOD_MASTER_KEY", "master-key-for-test")

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/logs", nil)
	req.Header.Set("X-Worker-Token", "master-key-for-test")
	guarded(t, &Handler{}).ServeHTTP(rec, req)

	if rec.Code == http.StatusForbidden {
		t.Error("a worker-token POST was blocked for a missing CSRF token; this breaks the data plane")
	}
}

// TestPublicEndpointsAreNotProtected: login has no session to protect yet, and
// webhooks and form posts are public by design with their own authentication.
// Requiring a token would make them unusable.
func TestPublicEndpointsAreNotProtected(t *testing.T) {
	withJWTConfig(t)

	for _, path := range []string{
		"/api/login",
		"/api/auth/2fa/login",
		"/api/webhooks/orders",
		"/api/forms/signup",
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, path, nil)
		guarded(t, &Handler{}).ServeHTTP(rec, req)

		if rec.Code == http.StatusForbidden {
			t.Errorf("%s was blocked for a missing CSRF token; it is public by design", path)
		}
	}
}

// TestTokenComparisonIsNotAPrefixMatch guards the sloppy implementation: a
// comparison that accepts a prefix, or ignores length, lets an attacker who can
// influence part of the value through.
func TestTokenComparisonIsNotAPrefixMatch(t *testing.T) {
	withJWTConfig(t)
	session := mintSession(t, time.Hour)

	for _, header := range []string{"abc", "abc1234", "", " abc123"} {
		rec := httptest.NewRecorder()
		req := csrfRequest(t, http.MethodPost, "/api/workflows", session, "abc123", header)
		guarded(t, &Handler{}).ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Errorf("header %q was accepted against cookie %q (got %d)", header, "abc123", rec.Code)
		}
	}
}

// TestIssueCSRFTokenSetsAReadableCookie: the UI has to be able to read it, so
// unlike the session cookie this one must NOT be HttpOnly. That is safe — the
// token is not a credential on its own, it only proves the request came from a
// context that could read same-origin cookies.
func TestIssueCSRFTokenSetsAReadableCookie(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/login", nil)

	tok := IssueCSRFToken(rec, req)
	if tok == "" {
		t.Fatal("IssueCSRFToken returned an empty token")
	}

	var found *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == CSRFCookieName {
			found = c
		}
	}
	if found == nil {
		t.Fatal("no CSRF cookie was set")
	}
	if found.HttpOnly {
		t.Error("the CSRF cookie is HttpOnly, so the UI cannot read it to echo in the header")
	}
	if found.Value != tok {
		t.Error("the cookie value does not match the returned token")
	}
	if len(tok) < 32 {
		t.Errorf("token is %d characters; too short to resist guessing", len(tok))
	}
}

// TestIssuedTokensAreUnique: a fixed token would be as good as no protection,
// since an attacker could simply hardcode it.
func TestIssuedTokensAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/login", nil)
		tok := IssueCSRFToken(rec, req)
		if seen[tok] {
			t.Fatalf("token %q was issued twice", tok)
		}
		seen[tok] = true
	}
}
