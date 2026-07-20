package handlers

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net"
	"net/url"
	"regexp"
	"strings"
)

const (
	// maxPluginWasmSize is the maximum allowed size for a plugin WASM file (10MB).
	MaxPluginWasmSize = 10 << 20
)

// VerifyWebhookSignature validates an incoming webhook signature against the
// configured secret using constant-time HMAC-SHA256 comparison. It accepts both
// the GitHub-style "sha256=<hex>" prefixed value and a bare hex digest.
func VerifyWebhookSignature(secret string, body []byte, signature string) bool {
	if secret == "" || signature == "" {
		return false
	}

	actual := signature
	if strings.HasPrefix(signature, "sha256=") {
		actual = strings.TrimPrefix(signature, "sha256=")
	}

	sig, err := hex.DecodeString(actual)
	if err != nil {
		return false
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := mac.Sum(nil)

	return hmac.Equal(sig, expected)
}

// ConstantTimeCompare compares two strings in constant time.
func ConstantTimeCompare(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

var pluginIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)

// ValidatePluginID ensures the identifier is safe to use as a filename and
// prevents directory traversal or shell injection.
func ValidatePluginID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	if strings.Contains(id, "..") {
		return false
	}
	return pluginIDPattern.MatchString(id)
}

// IsSafeWasmURL only permits http(s) URLs that do not target loopback,
// link-local, or otherwise private/internal addresses (SSRF protection).
func IsSafeWasmURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}

	host := u.Hostname()
	if host == "localhost" || host == "" {
		return false
	}

	ip := net.ParseIP(host)
	if ip != nil {
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsPrivate() {
			return false
		}
	}

	return true
}

var formPathPattern = regexp.MustCompile(`^[a-zA-Z0-9/_.-]+$`)

// IsValidFormPath reports whether the supplied path is safe to reflect back in
// a generated form page without risking XSS or other injection.
func IsValidFormPath(path string) bool {
	if path == "" || len(path) > 256 {
		return false
	}
	if strings.Contains(path, "..") {
		return false
	}
	return formPathPattern.MatchString(path)
}
