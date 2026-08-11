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
// Revocation through the real middleware.
//
// The Revoker unit tests prove the list behaves. They cannot prove the thing a
// user actually cares about: that after logging out, the cookie in their
// browser stops working. That takes the whole path — cookie, signature check,
// claims, revocation check — and it is the claim SECURITY.md makes.
// ---------------------------------------------------------------------------

// mintSessionWithJTI builds a signed session carrying a token ID and a session
// start, which is what the login handler now issues.
func mintSessionWithJTI(t *testing.T, jti, userID string, start time.Time) string {
	t.Helper()
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"id":       userID,
		"username": "alice",
		"role":     "Administrator",
		"jti":      jti,
		"sst":      start.Unix(),
		"exp":      time.Now().Add(time.Hour).Unix(),
	}).SignedString([]byte(testJWTSecret))
	if err != nil {
		t.Fatalf("signing token: %v", err)
	}
	return s
}

// TestRevokedCookieIsRejectedByTheMiddleware is the whole point of the feature.
func TestRevokedCookieIsRejectedByTheMiddleware(t *testing.T) {
	withJWTConfig(t)
	h := &Handler{}
	token := mintSessionWithJTI(t, "jti-1", "u1", time.Now().Add(-time.Minute))

	rec := httptest.NewRecorder()
	guarded(t, h).ServeHTTP(rec, cookieRequest("/api/workflows", token))
	if rec.Code == http.StatusUnauthorized {
		t.Fatalf("a valid session was rejected before any revocation (got %d)", rec.Code)
	}

	if err := h.SessionRevoker().Revoke(context.Background(), "jti-1", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	rec = httptest.NewRecorder()
	guarded(t, h).ServeHTTP(rec, cookieRequest("/api/workflows", token))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("the revoked session still works (got %d, want 401); "+
			"logging out does not actually end the session", rec.Code)
	}
}

// TestRevokeUserRejectsEveryCookieForThatUser: the password-change case, end to
// end. Another user's session must be untouched, or a password reset becomes an
// outage.
func TestRevokeUserRejectsEveryCookieForThatUser(t *testing.T) {
	withJWTConfig(t)
	h := &Handler{}
	started := time.Now().Add(-time.Minute)

	laptop := mintSessionWithJTI(t, "a", "u1", started)
	phone := mintSessionWithJTI(t, "b", "u1", started)
	other := mintSessionWithJTI(t, "c", "u2", started)

	if err := h.SessionRevoker().RevokeUser(context.Background(), "u1"); err != nil {
		t.Fatalf("RevokeUser: %v", err)
	}

	for name, tok := range map[string]string{"laptop": laptop, "phone": phone} {
		rec := httptest.NewRecorder()
		guarded(t, h).ServeHTTP(rec, cookieRequest("/api/workflows", tok))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s session survived a whole-user revocation (got %d, want 401)", name, rec.Code)
		}
	}

	rec := httptest.NewRecorder()
	guarded(t, h).ServeHTTP(rec, cookieRequest("/api/workflows", other))
	if rec.Code == http.StatusUnauthorized {
		t.Error("revoking one user's sessions logged a different user out")
	}
}

// TestRevocationIsCheckedBeforeRenewal. The middleware renews a session that is
// nearing expiry; if that ran first, a revoked session would be handed a fresh
// cookie on its way out — extending exactly the session being ended.
func TestRevocationIsCheckedBeforeRenewal(t *testing.T) {
	withJWTConfig(t)
	h := &Handler{}
	// Close enough to expiry that the middleware would want to renew it.
	token := mintSessionWithJTI(t, "jti-renew", "u1", time.Now().Add(-time.Minute))
	if err := h.SessionRevoker().Revoke(context.Background(), "jti-renew", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	rec := httptest.NewRecorder()
	guarded(t, h).ServeHTTP(rec, cookieRequest("/api/workflows", token))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked session accepted (got %d)", rec.Code)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == "hermod_session" && c.Value != "" {
			t.Error("a revoked session was issued a renewed cookie; revocation must be checked before renewal")
		}
	}
}

// TestPasswordChangeDoesNotLockTheUserOut reproduces a bug that fifteen unit
// tests missed and the first live run caught immediately.
//
// RevokeUser's cutoff has nanosecond precision; "sst" was issued as whole Unix
// seconds. So the login that follows a password change truncates to an instant
// *before* the cutoff and is treated as one of the sessions being revoked — the
// user is locked out of the account they just reset, every time.
//
// This mints and parses a real token rather than building claims by hand,
// because the truncation lives in exactly that round trip.
func TestPasswordChangeDoesNotLockTheUserOut(t *testing.T) {
	withJWTConfig(t)
	h := &Handler{}

	if err := h.SessionRevoker().RevokeUser(context.Background(), "u1"); err != nil {
		t.Fatalf("RevokeUser: %v", err)
	}

	// The user logs straight back in with their new password.
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256,
		NewSessionClaims("u1", "alice", "Administrator", nil)).SignedString([]byte(testJWTSecret))
	if err != nil {
		t.Fatalf("signing: %v", err)
	}

	claims, err := parseSessionClaims(token, []byte(testJWTSecret))
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	if h.SessionRevoker().IsRevoked(claims) {
		t.Error("the login that followed the password change was rejected; " +
			"the user is locked out of the account they just reset")
	}

	rec := httptest.NewRecorder()
	guarded(t, h).ServeHTTP(rec, cookieRequest("/api/me", token))
	if rec.Code == http.StatusUnauthorized {
		t.Error("the middleware rejected the session created after the password change")
	}
}

// TestRenewalDoesNotRewindTheSessionStart guards the second place this claim is
// decoded.
//
// Renewal re-reads "sst" from the raw token to carry the original login time
// forward. If that read truncates to whole seconds while the revocation cutoff
// keeps its fraction, a session that legitimately began just after a password
// change is rewound to before it — and the user is thrown out mid-session, up to
// an hour later, with nothing to connect it to the password change that caused
// it.
func TestRenewalDoesNotRewindTheSessionStart(t *testing.T) {
	withJWTConfig(t)
	h := &Handler{}

	if err := h.SessionRevoker().RevokeUser(context.Background(), "u1"); err != nil {
		t.Fatalf("RevokeUser: %v", err)
	}

	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256,
		NewSessionClaims("u1", "alice", "Administrator", nil)).SignedString([]byte(testJWTSecret))
	if err != nil {
		t.Fatalf("signing: %v", err)
	}

	// What renewal will carry forward as the session's start.
	carried := sessionStartOf(token)

	claims, err := parseSessionClaims(token, []byte(testJWTSecret))
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}

	if carried.Before(claims.SessionStart) {
		t.Errorf("renewal rewinds the session start by %v (claim %s, carried %s); "+
			"a session created after a password change would be revoked on its next renewal",
			claims.SessionStart.Sub(carried),
			claims.SessionStart.Format(time.RFC3339Nano), carried.Format(time.RFC3339Nano))
	}

	if h.SessionRevoker().IsRevoked(SessionClaims{UserID: "u1", TokenID: "x", SessionStart: carried}) {
		t.Error("after a renewal the session would be treated as revoked, " +
			"throwing the user out mid-session with no visible cause")
	}
}
