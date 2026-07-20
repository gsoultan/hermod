package http

import (
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/user/hermod/internal/api/handlers"
)

// clientIP extracts the best-effort client IP from the request, honoring the
// X-Forwarded-For header when present (e.g. behind a reverse proxy).
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || ip == "" {
		return r.RemoteAddr
	}
	return ip
}

// loginAttemptKey builds a stable key combining the username and client IP so
// that lockouts are scoped per account and origin.
func loginAttemptKey(username string, r *http.Request) string {
	return strings.ToLower(strings.TrimSpace(username)) + "|" + clientIP(r)
}

// CheckLoginLockout reports whether the given key is currently locked out and,
// if so, how long the caller must wait before retrying.
func (h *AuthHandler) CheckLoginLockout(key string) (bool, time.Duration) {
	val, ok := h.LoginAttempts.Load(key)
	if !ok {
		return false, 0
	}
	a := val.(*handlers.LoginAttempt)
	a.Mu.Lock()
	defer a.Mu.Unlock()

	if a.LockedUntil.IsZero() {
		return false, 0
	}
	if remaining := time.Until(a.LockedUntil); remaining > 0 {
		return true, remaining
	}
	return false, 0
}

// RegisterFailedLogin records a failed login attempt for the key and locks it
// out once MaxLoginAttempts is reached.
func (h *AuthHandler) RegisterFailedLogin(key string) {
	val, _ := h.LoginAttempts.LoadOrStore(key, &handlers.LoginAttempt{})
	a := val.(*handlers.LoginAttempt)
	a.Mu.Lock()
	defer a.Mu.Unlock()

	now := time.Now()
	// Reset the counter if the previous failure is older than the window
	// or a prior lockout has fully expired.
	if !a.LastFailure.IsZero() && now.Sub(a.LastFailure) > handlers.LoginAttemptWindow {
		a.Failures = 0
		a.LockedUntil = time.Time{}
	}

	a.Failures++
	a.LastFailure = now
	if a.Failures >= handlers.MaxLoginAttempts {
		a.LockedUntil = now.Add(handlers.LoginLockoutDuration)
	}

	h.StartRateLimitCleanup()
}

// ResetLoginAttempts clears any failed-login state for the key after a
// successful authentication.
func (h *AuthHandler) ResetLoginAttempts(key string) {
	h.LoginAttempts.Delete(key)
}

var (
	// ipv4Re matches IPv4 addresses (optionally followed by :port).
	ipv4Re = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}(?::\d{1,5})?\b`)
	// ipv6Re matches bracketed IPv6 host:port forms like [::1]:5432.
	ipv6Re = regexp.MustCompile(`\[[0-9A-Fa-f:]+\](?::\d{1,5})?`)
	// hostPortRe matches hostname:port pairs (e.g. db.internal:5432).
	hostPortRe = regexp.MustCompile(`\b[A-Za-z0-9][A-Za-z0-9.\-]*:\d{2,5}\b`)
)

// SanitizeDBError strips network identifiers (IP addresses, hostnames and
// ports) from a database connection error so they are never exposed to clients.
// This prevents leaking internal infrastructure details such as the database
// host and port through error messages.
func SanitizeDBError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	msg = ipv6Re.ReplaceAllString(msg, "[redacted]")
	msg = ipv4Re.ReplaceAllString(msg, "[redacted]")
	msg = hostPortRe.ReplaceAllString(msg, "[redacted]")
	return msg
}
