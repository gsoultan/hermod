package handlers

import (
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/user/hermod/internal/config"
)

// Session lifetime.
//
// The session is a stateless JWT, so expiring the cookie at logout ends it for
// that browser but does not revoke the token — a copy captured beforehand stays
// valid until it expires. Real revocation needs server-side session state,
// which is recorded in SECURITY.md as outstanding.
//
// What does not need that is the *size* of the window. A token used to be valid
// for 24 hours, which is exactly how long a stolen one stayed useful. Cutting
// that to an hour and renewing on activity leaves the experience unchanged —
// an active session never expires under the user — while reducing a captured
// token from a day of access to about an hour.
//
// Renewal alone would let a session live forever, so there are two bounds.
const (
	// SessionTTL is how long any single token is valid.
	SessionTTL = time.Hour

	// MaxSessionAge caps how long a session may be renewed for in total,
	// measured from the original login. Past it, renewal stops and the user
	// authenticates again — otherwise sliding renewal is an eternal session.
	MaxSessionAge = 24 * time.Hour

	// SessionStartClaim carries the original login time across renewals. It is
	// what MaxSessionAge is measured against; without it each renewal would
	// reset the clock.
	SessionStartClaim = "sst"

	// renewalThreshold is the fraction of SessionTTL remaining below which a
	// token is renewed. Half is a deliberate compromise: renewing on every
	// request signs a token per call for nothing, and renewing only at the last
	// moment leaves no slack for a client that is briefly offline.
	renewalThreshold = 2
)

// sessionTTL returns the configured token lifetime.
//
// Overridable because the right value depends on the deployment — a kiosk wants
// minutes, an internal tool may want longer — and because a fixed hour would
// otherwise be a reason not to adopt this at all.
func sessionTTL() time.Duration {
	if v := strings.TrimSpace(os.Getenv("HERMOD_SESSION_TTL")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return SessionTTL
}

// maxSessionAge returns the absolute cap on a renewed session.
func maxSessionAge() time.Duration {
	if v := strings.TrimSpace(os.Getenv("HERMOD_MAX_SESSION_AGE")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return MaxSessionAge
}

// NewSessionClaims builds the claim set for a freshly issued session. Callers
// that mint a token at login should use this so every issue site agrees on the
// lifetime and carries the session-start marker renewal depends on.
func NewSessionClaims(userID, username, role string, vhosts []string) jwt.MapClaims {
	now := time.Now()
	return jwt.MapClaims{
		"id":              userID,
		"username":        username,
		"role":            role,
		"vhosts":          vhosts,
		"exp":             now.Add(sessionTTL()).Unix(),
		SessionStartClaim: now.Unix(),
	}
}

// SessionCookieMaxAge is the cookie lifetime that matches the token's.
func SessionCookieMaxAge() int { return int(sessionTTL().Seconds()) }

// maybeRenewSession issues a fresh token when the current one is past half its
// life, so an active user is never logged out mid-work by the short TTL.
//
// It returns true when it wrote a cookie. Renewal is skipped when the session
// has been running longer than maxSessionAge, which is the bound that stops
// sliding renewal from becoming permanent.
func (h *Handler) maybeRenewSession(w http.ResponseWriter, r *http.Request, claims SessionClaims, rawToken string) bool {
	// Only the cookie is renewable. A Bearer client manages its own token, and
	// rewriting a cookie for a request that did not send one is meaningless.
	if !authenticatedByCookie(r) {
		return false
	}
	if claims.ExpiresAt == nil {
		return false
	}

	ttl := sessionTTL()
	remaining := time.Until(claims.ExpiresAt.Time)
	if remaining <= 0 || remaining > ttl/renewalThreshold {
		// Either already expired — which the caller rejects — or still fresh.
		return false
	}

	// A token issued before this change has no session-start claim. Treating it
	// as newly started is deliberate: the alternative is logging everyone out on
	// deploy, and the absolute cap still applies from now on.
	start := sessionStartOf(rawToken)
	if !start.IsZero() && time.Since(start) > maxSessionAge() {
		return false
	}
	if start.IsZero() {
		start = time.Now()
	}

	dbCfg, err := config.LoadDBConfig()
	if err != nil || strings.TrimSpace(dbCfg.JWTSecret) == "" {
		return false
	}

	renewed := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"id":              claims.UserID,
		"username":        claims.Username,
		"role":            claims.Role,
		"vhosts":          claims.VHosts,
		"exp":             time.Now().Add(ttl).Unix(),
		SessionStartClaim: start.Unix(),
	})
	signed, err := renewed.SignedString([]byte(dbCfg.JWTSecret))
	if err != nil {
		// Failing to renew is not failing the request: the current token is
		// still valid, and the user simply re-authenticates when it expires.
		return false
	}

	http.SetCookie(w, SessionCookie(r, signed, int(ttl.Seconds())))
	return true
}

// SessionCookieName is the only cookie carrying a Hermod session.
const SessionCookieName = "hermod_session"

// SessionCookie builds the session cookie. maxAge is the lifetime in seconds,
// or -1 to delete.
//
// One builder for every site that sets or clears it — login, 2FA, renewal and
// logout. A cookie is only replaced when its name, path and domain match, so a
// logout or renewal that spelled any of them differently would leave the
// original in place and silently fail to take effect.
//
// gosec cannot prove Secure is set because it is derived from the request
// scheme. That is deliberate: pinning it true would stop the cookie working
// over plain HTTP on localhost, which is how the dev stack runs. It is forced
// true for SameSite=None, where browsers require it.
//
//nolint:gosec // G124: Secure is request-derived; see above.
func SessionCookie(r *http.Request, value string, maxAge int) *http.Cookie {
	secure := r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
	ss := SameSiteFromEnv()
	if ss == http.SameSiteNoneMode {
		secure = true
	}
	return &http.Cookie{
		Name:     SessionCookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: ss,
		MaxAge:   maxAge,
	}
}

// sessionStartOf reads the session-start claim without re-validating the token;
// the caller has already done that.
func sessionStartOf(rawToken string) time.Time {
	parser := jwt.NewParser()
	var claims jwt.MapClaims
	if _, _, err := parser.ParseUnverified(rawToken, &claims); err != nil {
		return time.Time{}
	}
	switch v := claims[SessionStartClaim].(type) {
	case float64:
		return time.Unix(int64(v), 0)
	case int64:
		return time.Unix(v, 0)
	default:
		return time.Time{}
	}
}
