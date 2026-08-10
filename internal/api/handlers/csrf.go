package handlers

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strings"
)

// CSRF protection, double-submit style.
//
// The API authenticates with a cookie, and a browser attaches cookies to
// cross-site requests it was tricked into making. SameSite is currently the
// only thing between that and a state change — adequate at Lax or Strict, and
// nothing at all the moment a deployment sets None to allow cross-origin
// embedding.
//
// Double submit: the server issues a readable token in a cookie, the client
// echoes it in a header, and the two must match. It works because an attacker
// on another origin can make the browser *send* the cookie but cannot read it
// to populate the header — and cannot set custom headers cross-origin at all.
const (
	// CSRFCookieName holds the token. Deliberately NOT HttpOnly: the UI has to
	// read it to echo it back. That is safe because the token is not a
	// credential on its own — it only proves the request came from a context
	// that could read same-origin cookies.
	CSRFCookieName = "hermod_csrf"

	// CSRFHeaderName is where the client echoes it.
	CSRFHeaderName = "X-CSRF-Token"

	// csrfTokenBytes is the entropy behind each token. 32 bytes is far past
	// guessable and costs nothing.
	csrfTokenBytes = 32
)

// IssueCSRFToken generates a token, sets it as a readable cookie, and returns
// it. Call it wherever a session begins.
//
// Secure mirrors the session cookie's own logic so the pair behave alike across
// HTTP and HTTPS deployments; SameSite is deliberately Lax rather than Strict,
// because the token has to survive a top-level navigation back into the app or
// the first state-changing request after one fails.
func IssueCSRFToken(w http.ResponseWriter, r *http.Request) string {
	buf := make([]byte, csrfTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand failing is not a condition to paper over with a weaker
		// token: an attacker-predictable value is the same as no protection.
		// Returning empty means no cookie is set and every state change is
		// rejected, which is the safe direction.
		return ""
	}
	token := base64.RawURLEncoding.EncodeToString(buf)

	secure := r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
	ss := SameSiteFromEnv()
	if ss == http.SameSiteNoneMode {
		secure = true
	}

	// nolint:gosec // G124: HttpOnly is false on purpose. Double submit works
	// precisely because the client can read this value and echo it in a header
	// that a cross-origin attacker cannot set. Making it HttpOnly would leave
	// the client unable to read it and disable the protection entirely. The
	// token is not a credential: on its own it grants nothing.
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // see above
		Name:     CSRFCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: false, // the UI must read this; see above
		Secure:   secure,
		SameSite: ss,
		MaxAge:   24 * 60 * 60,
	})
	return token
}

// ClearCSRFToken expires the token cookie. Called alongside logout so a stale
// token does not outlive the session it belonged to.
func ClearCSRFToken(w http.ResponseWriter, r *http.Request) {
	secure := r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
	ss := SameSiteFromEnv()
	if ss == http.SameSiteNoneMode {
		secure = true
	}
	// Attributes must match the cookie being replaced or the browser keeps the
	// original; HttpOnly is false for the same reason as when it was issued.
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: see IssueCSRFToken
		Name:     CSRFCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: false,
		Secure:   secure,
		SameSite: ss,
		MaxAge:   -1,
	})
}

// isStateChanging reports whether a method can alter state. GET and HEAD are
// read paths; requiring a token on them buys nothing and breaks navigation.
// OPTIONS is a preflight and never carries a body.
func isStateChanging(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	default:
		return true
	}
}

// checkCSRF validates the double-submit pair.
//
// It is only meaningful for requests authenticated by cookie. A request
// carrying Authorization or X-Worker-Token is not forgeable cross-origin —
// an attacker cannot set those headers — so requiring a second one would break
// every CLI, worker and integration for no gain. The caller decides that; this
// function assumes it has already been decided.
func checkCSRF(r *http.Request) bool {
	cookie, err := r.Cookie(CSRFCookieName)
	if err != nil || cookie.Value == "" {
		return false
	}
	header := r.Header.Get(CSRFHeaderName)
	if header == "" {
		return false
	}
	// Constant time, and length-checked by ConstantTimeCompare returning 0 for
	// differing lengths — so this is not a prefix match.
	return subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(header)) == 1
}
