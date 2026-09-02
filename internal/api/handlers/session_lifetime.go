package handlers

import (
	"math"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/gsoultan/Hermod/internal/config"
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
		"id":       userID,
		"username": username,
		"role":     role,
		"vhosts":   vhosts,
		"exp":      now.Add(sessionTTL()).Unix(),
		// jti names this session so it can be revoked individually. Without it
		// the only instrument is RevokeUser, which ends every session the user
		// holds — far blunter than "log this browser out".
		"jti":             uuid.NewString(),
		SessionStartClaim: numericDate(now),
	}
}

// numericDate renders t as a JWT NumericDate keeping sub-second resolution.
//
// Whole seconds are not enough here. RevokeUser's cutoff is an instant with
// nanosecond precision, and IsRevoked ends every session that began before it.
// Truncating this claim down to the second puts the login that *follows* a
// password change before the cutoff that the change installed, so the user is
// locked out of the account they just reset — reliably, not occasionally.
//
// RFC 7519 §2 defines NumericDate as a JSON number and explicitly permits
// non-integer values, so a fractional second is a legal claim rather than a
// private encoding. Tokens carrying the older whole-second form still decode to
// the same instant they always did.
func numericDate(t time.Time) float64 {
	// Not UnixNano()/1e9: nanoseconds since the epoch is a nineteen-digit
	// integer and float64 carries about sixteen, so that conversion rounds the
	// instant by a few hundred nanoseconds — in either direction. Splitting the
	// value keeps the whole seconds exact and spends the mantissa on the
	// fraction instead.
	return float64(t.Unix()) + float64(t.Nanosecond())/float64(time.Second)
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

	// The renewed token keeps the same jti. A renewal is the same session
	// continuing, so minting a new id would let it slip out from under a
	// revocation simply by staying active.
	renewed := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"id":              claims.UserID,
		"username":        claims.Username,
		"role":            claims.Role,
		"vhosts":          claims.VHosts,
		"exp":             time.Now().Add(ttl).Unix(),
		"jti":             claims.TokenID,
		SessionStartClaim: numericDate(start),
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
		return timeFromNumericDate(v)
	case int64:
		return timeFromNumericDate(float64(v))
	default:
		return time.Time{}
	}
}

// timeFromNumericDate is the inverse of numericDate, and the only place this
// claim is turned back into an instant.
//
// Both sites that read it have to agree. Renewal carries the session start
// forward, so a decoder that dropped the fraction here would rewind the session
// to the start of its second on every renewal — quietly moving a session that
// began after a password change to before it, and throwing the user out an hour
// later with nothing pointing at the cause.
func timeFromNumericDate(v float64) time.Time {
	sec, frac := math.Modf(v)
	return time.Unix(int64(sec), int64(frac*float64(time.Second)))
}
