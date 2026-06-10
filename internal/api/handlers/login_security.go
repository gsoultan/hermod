package handlers

import (
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	// MaxLoginAttempts is the number of consecutive failed login attempts
	// allowed before an account/IP combination is temporarily locked out.
	MaxLoginAttempts = 5

	// LoginLockoutDuration is how long a locked account/IP must wait before
	// it is allowed to attempt logging in again.
	LoginLockoutDuration = 15 * time.Minute

	// LoginAttemptWindow is the period of inactivity after which the failed
	// attempt counter is reset automatically.
	LoginAttemptWindow = 15 * time.Minute
)

// loginAttempt holds the failed-login bookkeeping for a single key.
type loginAttempt struct {
	mu          sync.Mutex
	failures    int
	lockedUntil time.Time
	lastFailure time.Time
}

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
func (h *Handler) CheckLoginLockout(key string) (bool, time.Duration) {
	val, ok := h.LoginAttempts.Load(key)
	if !ok {
		return false, 0
	}
	a := val.(*loginAttempt)
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.lockedUntil.IsZero() {
		return false, 0
	}
	if remaining := time.Until(a.lockedUntil); remaining > 0 {
		return true, remaining
	}
	return false, 0
}

// RegisterFailedLogin records a failed login attempt for the key and locks it
// out once MaxLoginAttempts is reached.
func (h *Handler) RegisterFailedLogin(key string) {
	val, _ := h.LoginAttempts.LoadOrStore(key, &loginAttempt{})
	a := val.(*loginAttempt)
	a.mu.Lock()
	defer a.mu.Unlock()

	now := time.Now()
	// Reset the counter if the previous failure is older than the window
	// or a prior lockout has fully expired.
	if !a.lastFailure.IsZero() && now.Sub(a.lastFailure) > LoginAttemptWindow {
		a.failures = 0
		a.lockedUntil = time.Time{}
	}

	a.failures++
	a.lastFailure = now
	if a.failures >= MaxLoginAttempts {
		a.lockedUntil = now.Add(LoginLockoutDuration)
	}

	h.StartRateLimitCleanup()
}

// ResetLoginAttempts clears any failed-login state for the key after a
// successful authentication.
func (h *Handler) ResetLoginAttempts(key string) {
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
