package handlers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestVerifyWebhookSignature(t *testing.T) {
	secret := "topsecret"
	body := []byte(`{"event":"push"}`)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	valid := hex.EncodeToString(mac.Sum(nil))

	tests := []struct {
		name      string
		secret    string
		signature string
		want      bool
	}{
		{"ValidBare", secret, valid, true},
		{"ValidPrefixed", secret, "sha256=" + valid, true},
		{"WrongSignature", secret, "deadbeef", false},
		{"EmptySignature", secret, "", false},
		{"EmptySecret", "", valid, false},
		{"NonHex", secret, "not-hex-zz", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := verifyWebhookSignature(tc.secret, body, tc.signature); got != tc.want {
				t.Errorf("verifyWebhookSignature(%q) = %v; want %v", tc.signature, got, tc.want)
			}
		})
	}
}

func TestValidatePluginID(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want bool
	}{
		{"Simple", "my-plugin", true},
		{"WithDotVersion", "my.plugin_1.2", true},
		{"Empty", "", false},
		{"Traversal", "../etc/passwd", false},
		{"Slash", "a/b", false},
		{"DotDot", "a..b", false},
		{"Backslash", "a\\b", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := validatePluginID(tc.id); got != tc.want {
				t.Errorf("validatePluginID(%q) = %v; want %v", tc.id, got, tc.want)
			}
		})
	}
}

func TestIsSafeWasmURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want bool
	}{
		{"HTTPS", "https://cdn.example.com/p.wasm", true},
		{"HTTP", "http://cdn.example.com/p.wasm", true},
		{"Loopback", "http://127.0.0.1/p.wasm", false},
		{"Localhost IP v6", "http://[::1]/p.wasm", false},
		{"Private", "http://10.0.0.5/p.wasm", false},
		{"LinkLocal", "http://169.254.169.254/latest", false},
		{"FileScheme", "file:///etc/passwd", false},
		{"NoHost", "https://", false},
		{"Garbage", "://bad", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isSafeWasmURL(tc.url); got != tc.want {
				t.Errorf("isSafeWasmURL(%q) = %v; want %v", tc.url, got, tc.want)
			}
		})
	}
}

func TestIsValidFormPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{"Simple", "contact", true},
		{"Nested", "team/contact", true},
		{"Empty", "", false},
		{"XSSQuote", `"></script>`, false},
		{"Traversal", "../secret", false},
		{"Space", "a b", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isValidFormPath(tc.path); got != tc.want {
				t.Errorf("isValidFormPath(%q) = %v; want %v", tc.path, got, tc.want)
			}
		})
	}
}

func TestIsCORSOriginAllowed(t *testing.T) {
	allowed := []string{"https://app.example.com", "https://admin.example.com"}
	tests := []struct {
		name         string
		origin       string
		list         []string
		wantOK       bool
		wantWildcard bool
	}{
		{"Match", "https://app.example.com", allowed, true, false},
		{"CaseInsensitive", "https://APP.example.com", allowed, true, false},
		{"NotInList", "https://evil.com", allowed, false, false},
		{"EmptyList", "https://app.example.com", nil, false, false},
		{"Wildcard", "https://anything.com", []string{"*"}, true, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ok, wildcard := isCORSOriginAllowed(tc.origin, tc.list)
			if ok != tc.wantOK || wildcard != tc.wantWildcard {
				t.Errorf("isCORSOriginAllowed(%q) = (%v,%v); want (%v,%v)", tc.origin, ok, wildcard, tc.wantOK, tc.wantWildcard)
			}
		})
	}
}

func TestCorsMiddlewareDeniesUnknownOrigin(t *testing.T) {
	h := &Handler{}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := h.CorsMiddleware(next)

	// No allow-list configured: arbitrary origin must NOT be reflected.
	req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
	req.Header.Set("Origin", "https://evil.com")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("expected no Access-Control-Allow-Origin, got %q", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("expected no Access-Control-Allow-Credentials, got %q", got)
	}
}

func TestCorsMiddlewareAllowsConfiguredOrigin(t *testing.T) {
	t.Setenv("HERMOD_ALLOWED_ORIGINS", "https://app.example.com")
	h := &Handler{}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := h.CorsMiddleware(next)

	req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
	req.Header.Set("Origin", "https://app.example.com")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Errorf("expected reflected origin, got %q", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("expected credentials true, got %q", got)
	}
}

func TestIsOriginAllowedExactHost(t *testing.T) {
	allowed := "example.com"
	tests := []struct {
		name    string
		origin  string
		referer string
		want    bool
	}{
		{"ExactOrigin", "https://example.com", "", true},
		{"SpoofedSubdomain", "https://evil-example.com.attacker.io", "", false},
		{"RefererMatch", "", "https://example.com/page", true},
		{"NoMatch", "https://other.com", "", false},
	}

	h := &Handler{}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := h.IsOriginAllowed(tc.origin, tc.referer, allowed); got != tc.want {
				t.Errorf("IsOriginAllowed(%q,%q) = %v; want %v", tc.origin, tc.referer, got, tc.want)
			}
		})
	}
}

func TestIsRateLimitedConcurrent(t *testing.T) {
	h := &Handler{}
	limit := 100
	const goroutines = 50
	const perG = 10 // 500 total attempts, only 100 may pass

	var mu sync.Mutex
	allowed := 0
	var wg sync.WaitGroup
	for range goroutines {
		wg.Go(func() {
			for range perG {
				req := httptest.NewRequest(http.MethodPost, "/api/forms/x", nil)
				req.RemoteAddr = "203.0.113.7:12345"
				if !h.IsRateLimited(req, "src-1", limit) {
					mu.Lock()
					allowed++
					mu.Unlock()
				}
			}
		})
	}
	wg.Wait()

	if allowed != limit {
		t.Errorf("expected exactly %d allowed requests, got %d", limit, allowed)
	}
}
