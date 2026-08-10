package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ---------------------------------------------------------------------------
// Authenticating the UI's streams.
//
// These endpoints used to accept the session JWT as a `?token=` query
// parameter. That is why a copy of a 24-hour session token had to live in
// localStorage: a browser cannot set headers on `new WebSocket(url)`, so the
// only place the UI could put a credential was the URL — and to put it there it
// had to be able to read it from JavaScript. Which made the HttpOnly cookie
// decorative, because any XSS could lift the token out of storage.
//
// None of it was necessary. A browser sends cookies on a same-origin WebSocket
// handshake exactly as it does on any other request (RFC 6455 §4.1), and three
// of the UI's own sockets — sinks, sources, layout — had always relied on that
// and worked. The other five carried a token for no reason.
//
// So the UI sends no credential in a stream URL, and these endpoints refuse
// one. The refusal is the part that matters: without it the old pattern can
// come back in a single line and nobody notices until an audit.
// ---------------------------------------------------------------------------

const testJWTSecret = "testsecret"

// withJWTConfig points config.LoadDBConfig at a scratch config carrying a known
// signing secret, so these tests mint tokens the middleware will actually trust.
func withJWTConfig(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "db_config.yaml"),
		[]byte("type: sqlite\nconn: \"file::memory:\"\njwt_secret: "+testJWTSecret+"\n"), 0o600); err != nil {
		t.Fatalf("writing db_config.yaml: %v", err)
	}
	t.Setenv("HERMOD_CONFIG_DIR", dir)
	t.Setenv("HERMOD_JWT_SECRET", testJWTSecret)
}

// mintSession builds a signed session token of the kind /api/login issues.
func mintSession(t *testing.T, ttl time.Duration) string {
	t.Helper()
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"id":       "u1",
		"username": "alice",
		"role":     "Administrator",
		"exp":      time.Now().Add(ttl).Unix(),
	}).SignedString([]byte(testJWTSecret))
	if err != nil {
		t.Fatalf("signing token: %v", err)
	}
	return s
}

// queryRequest presents tok in the query string, the way the UI used to.
func queryRequest(path, tok string) *http.Request {
	return httptest.NewRequestWithContext(context.Background(), http.MethodGet, path+"?token="+tok, nil)
}

// cookieRequest presents tok the way the browser does on a WebSocket handshake.
func cookieRequest(path, tok string) *http.Request {
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)
	r.AddCookie(&http.Cookie{Name: "hermod_session", Value: tok})
	return r
}

// TestUIStreamsRejectTokenInQuery is the guard that keeps the credential out of
// URLs — and therefore out of access logs, proxy logs and browser history.
func TestUIStreamsRejectTokenInQuery(t *testing.T) {
	withJWTConfig(t)
	session := mintSession(t, 24*time.Hour)

	for _, path := range []string{
		"/api/ws/live",
		"/api/ws/status",
		"/api/ws/dashboard",
		"/api/ws/logs",
		"/api/ws/debugger",
		"/api/notifications/sse",
	} {
		rec := httptest.NewRecorder()
		guarded(t, &Handler{}).ServeHTTP(rec, queryRequest(path, session))

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s accepted a session token in the query string (got %d); "+
				"the UI must authenticate streams with the session cookie", path, rec.Code)
		}
	}
}

// TestUIStreamsAcceptSessionCookie is the other half: refusing the query
// parameter is only correct because the cookie works. If this fails, every live
// view in the UI is dead.
func TestUIStreamsAcceptSessionCookie(t *testing.T) {
	withJWTConfig(t)
	session := mintSession(t, 24*time.Hour)

	for _, path := range []string{
		"/api/ws/live",
		"/api/ws/status",
		"/api/ws/dashboard",
		"/api/ws/logs",
		"/api/ws/debugger",
		"/api/notifications/sse",
	} {
		rec := httptest.NewRecorder()
		guarded(t, &Handler{}).ServeHTTP(rec, cookieRequest(path, session))

		if rec.Code == http.StatusUnauthorized {
			t.Errorf("%s rejected a valid session cookie; the UI cannot open this stream at all", path)
		}
	}
}

// TestNormalAPIUnaffected is the regression guard: the rule above is scoped to
// stream endpoints and must not touch ordinary authenticated traffic.
func TestNormalAPIUnaffected(t *testing.T) {
	withJWTConfig(t)
	session := mintSession(t, 24*time.Hour)

	for _, path := range []string{"/api/workflows", "/api/users", "/api/settings"} {
		rec := httptest.NewRecorder()
		guarded(t, &Handler{}).ServeHTTP(rec, cookieRequest(path, session))

		if rec.Code == http.StatusUnauthorized {
			t.Errorf("%s rejected a normal session cookie", path)
		}
	}
}

// TestExpiredSessionRejectedOnStreams: the cookie path must still validate the
// token rather than merely notice a cookie is present.
func TestExpiredSessionRejectedOnStreams(t *testing.T) {
	withJWTConfig(t)
	expired := mintSession(t, -time.Minute)

	rec := httptest.NewRecorder()
	guarded(t, &Handler{}).ServeHTTP(rec, cookieRequest("/api/ws/logs", expired))

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("an expired session was accepted on a stream endpoint (got %d)", rec.Code)
	}
}

// TestIntegrationWSEndpointsKeepAcceptingQueryTokens documents a deliberate
// carve-out rather than an oversight.
//
// /api/ws/in and /api/ws/out are integration endpoints with no auth of their
// own: an external producer authenticates through AuthMiddleware, and a
// non-browser client that cannot set headers has only the query parameter. The
// UI does not use them, so they are not part of this change and tightening them
// would be a silent break for someone else's running integration.
//
// The residual — a credential in a URL on those two paths — is recorded in
// SECURITY.md rather than left implicit.
func TestIntegrationWSEndpointsKeepAcceptingQueryTokens(t *testing.T) {
	withJWTConfig(t)
	session := mintSession(t, 24*time.Hour)

	for _, path := range []string{"/api/ws/in/orders", "/api/ws/out/wf-1"} {
		rec := httptest.NewRecorder()
		guarded(t, &Handler{}).ServeHTTP(rec, queryRequest(path, session))

		if rec.Code == http.StatusUnauthorized {
			t.Errorf("%s rejected a query token; this endpoint has no other auth "+
				"mechanism for non-browser clients", path)
		}
	}
}
