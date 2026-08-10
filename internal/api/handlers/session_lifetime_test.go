package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ---------------------------------------------------------------------------
// Session lifetime.
//
// The session is a stateless JWT, so expiring the cookie at logout ends it for
// that browser but does not revoke the token: a copy captured beforehand stays
// valid until it expires. True revocation needs server-side session state.
//
// What does not need that is the size of the window. A token was valid for 24
// hours, which is how long a stolen one stayed useful. Cutting it to an hour
// and renewing on activity leaves the user experience unchanged — an active
// session never expires under them — while reducing a captured token from a
// day of access to roughly an hour.
//
// Two bounds, because sliding renewal alone would let a session live forever:
//
//	SessionTTL      how long any single token is valid.
//	MaxSessionAge   how long a session may be renewed for in total, measured
//	                from the original login. Past it, renewal stops and the
//	                user authenticates again.
// ---------------------------------------------------------------------------

// mintSessionWithClaims builds a token with explicit timing claims.
func mintSessionWithClaims(t *testing.T, exp, sessionStart time.Time) string {
	t.Helper()
	claims := jwt.MapClaims{
		"id":       "u1",
		"username": "alice",
		"role":     "Administrator",
		"exp":      exp.Unix(),
	}
	if !sessionStart.IsZero() {
		claims[SessionStartClaim] = sessionStart.Unix()
	}
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(testJWTSecret))
	if err != nil {
		t.Fatalf("signing: %v", err)
	}
	return s
}

func sessionCookieFrom(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == "hermod_session" {
			return c
		}
	}
	return nil
}

// TestSessionIsRenewedWhenPastHalfItsLife: an active user must never be logged
// out mid-work by the shorter TTL.
func TestSessionIsRenewedWhenPastHalfItsLife(t *testing.T) {
	withJWTConfig(t)

	// Two minutes left on a one-hour TTL: well past halfway.
	tok := mintSessionWithClaims(t, time.Now().Add(2*time.Minute), time.Now().Add(-58*time.Minute))

	rec := httptest.NewRecorder()
	guarded(t, &Handler{}).ServeHTTP(rec, cookieRequest("/api/workflows", tok))

	if rec.Code == http.StatusUnauthorized {
		t.Fatal("a still-valid session was rejected")
	}
	c := sessionCookieFrom(rec)
	if c == nil {
		t.Fatal("no refreshed session cookie was issued; an active user would be logged out when this token expires")
	}
	if c.Value == tok {
		t.Error("the cookie was re-sent unchanged rather than renewed")
	}
}

// TestFreshSessionIsNotRenewed: re-issuing on every request would mean signing
// a token per call and resetting the cookie constantly for no benefit.
func TestFreshSessionIsNotRenewed(t *testing.T) {
	withJWTConfig(t)

	// Just issued: nowhere near halfway.
	tok := mintSessionWithClaims(t, time.Now().Add(SessionTTL), time.Now())

	rec := httptest.NewRecorder()
	guarded(t, &Handler{}).ServeHTTP(rec, cookieRequest("/api/workflows", tok))

	if rec.Code == http.StatusUnauthorized {
		t.Fatal("a fresh session was rejected")
	}
	if c := sessionCookieFrom(rec); c != nil {
		t.Error("a fresh session was renewed; this signs a token on every request for nothing")
	}
}

// TestRenewalStopsAtTheAbsoluteAge is the bound that stops sliding renewal
// becoming an eternal session. Past MaxSessionAge the user logs in again.
func TestRenewalStopsAtTheAbsoluteAge(t *testing.T) {
	withJWTConfig(t)

	// Renewable on its own terms, but the session began well beyond the cap.
	tok := mintSessionWithClaims(t,
		time.Now().Add(2*time.Minute),
		time.Now().Add(-MaxSessionAge-time.Hour))

	rec := httptest.NewRecorder()
	guarded(t, &Handler{}).ServeHTTP(rec, cookieRequest("/api/workflows", tok))

	if c := sessionCookieFrom(rec); c != nil {
		t.Error("a session past the absolute age was renewed; it would never end")
	}
}

// TestExpiredSessionIsStillRejected: renewal must not resurrect a dead token.
func TestExpiredSessionIsStillRejected(t *testing.T) {
	withJWTConfig(t)

	tok := mintSessionWithClaims(t, time.Now().Add(-time.Minute), time.Now().Add(-time.Hour))

	rec := httptest.NewRecorder()
	guarded(t, &Handler{}).ServeHTTP(rec, cookieRequest("/api/workflows", tok))

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("an expired token was accepted (got %d)", rec.Code)
	}
	if c := sessionCookieFrom(rec); c != nil && c.MaxAge > 0 {
		t.Error("an expired token was renewed")
	}
}

// TestTokenWithoutASessionStartIsTreatedAsNew covers the upgrade: tokens issued
// before this change carry no session-start claim. Rejecting or refusing to
// renew them would log everyone out on deploy; treating them as freshly started
// gives them one full MaxSessionAge from now, which is the gentle direction.
func TestTokenWithoutASessionStartIsTreatedAsNew(t *testing.T) {
	withJWTConfig(t)

	tok := mintSessionWithClaims(t, time.Now().Add(2*time.Minute), time.Time{})

	rec := httptest.NewRecorder()
	guarded(t, &Handler{}).ServeHTTP(rec, cookieRequest("/api/workflows", tok))

	if rec.Code == http.StatusUnauthorized {
		t.Fatal("a pre-existing session was rejected on upgrade; everyone would be logged out")
	}
	if c := sessionCookieFrom(rec); c == nil {
		t.Error("a pre-existing session was not renewed, so it will expire without warning")
	}
}

// TestRenewalKeepsTheUserIdentity: a renewed token that lost the role would
// silently demote the user, or worse, promote them.
func TestRenewalKeepsTheUserIdentity(t *testing.T) {
	withJWTConfig(t)

	tok := mintSessionWithClaims(t, time.Now().Add(2*time.Minute), time.Now().Add(-30*time.Minute))

	rec := httptest.NewRecorder()
	guarded(t, &Handler{}).ServeHTTP(rec, cookieRequest("/api/workflows", tok))

	c := sessionCookieFrom(rec)
	if c == nil {
		t.Fatal("no renewed cookie")
	}

	claims, err := parseSessionClaims(c.Value, []byte(testJWTSecret))
	if err != nil {
		t.Fatalf("the renewed token does not parse: %v", err)
	}
	if claims.UserID != "u1" || claims.Username != "alice" || claims.Role != "Administrator" {
		t.Errorf("renewal changed the identity: %+v", claims)
	}
}

// TestReadsAlsoRenew: browsing is activity. Renewing only on writes would
// expire a session under someone who spent an hour reading dashboards.
func TestReadsAlsoRenew(t *testing.T) {
	withJWTConfig(t)

	tok := mintSessionWithClaims(t, time.Now().Add(2*time.Minute), time.Now().Add(-30*time.Minute))

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/workflows", nil)
	req.AddCookie(&http.Cookie{Name: "hermod_session", Value: tok})
	guarded(t, &Handler{}).ServeHTTP(rec, req)

	if c := sessionCookieFrom(rec); c == nil {
		t.Error("a read did not renew the session")
	}
}
