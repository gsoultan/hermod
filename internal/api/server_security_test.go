package api

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestExtractBearerOrCookie(t *testing.T) {
	// Header bearer
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Authorization", "Bearer token123")
	if tok, ok := extractBearerOrCookie(r); !ok || tok != "token123" {
		t.Fatalf("expected token from header, got ok=%v tok=%q", ok, tok)
	}

	// Cookie fallback
	r2 := httptest.NewRequest(http.MethodGet, "/", nil)
	r2.AddCookie(&http.Cookie{Name: "hermod_session", Value: "cookieTok"})
	if tok, ok := extractBearerOrCookie(r2); !ok || tok != "cookieTok" {
		t.Fatalf("expected token from cookie, got ok=%v tok=%q", ok, tok)
	}

	// Missing
	r3 := httptest.NewRequest(http.MethodGet, "/", nil)
	if _, ok := extractBearerOrCookie(r3); ok {
		t.Fatalf("expected no token")
	}
}

func TestIsPrivateIP(t *testing.T) {
	privs := []string{"10.1.2.3", "192.168.1.1", "127.0.0.1", "169.254.10.1", "fc00::1", "fe80::1"}
	for _, s := range privs {
		if !isPrivateIP(net.ParseIP(s)) {
			t.Fatalf("expected private: %s", s)
		}
	}
	publics := []string{"1.1.1.1", "8.8.8.8", "2001:4860:4860::8888"}
	for _, s := range publics {
		if isPrivateIP(net.ParseIP(s)) {
			t.Fatalf("expected public: %s", s)
		}
	}
}

func TestWebSocketCheckOrigin(t *testing.T) {
	// Save and restore env
	oldAllow := os.Getenv("HERMOD_CORS_ALLOW_ORIGINS")
	oldEnv := os.Getenv("HERMOD_ENV")
	defer func() {
		_ = os.Setenv("HERMOD_CORS_ALLOW_ORIGINS", oldAllow)
		_ = os.Setenv("HERMOD_ENV", oldEnv)
	}()

	_ = os.Setenv("HERMOD_ENV", "production")
	_ = os.Setenv("HERMOD_CORS_ALLOW_ORIGINS", "https://example.com")

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Origin", "https://example.com")
	if !upgrader.CheckOrigin(r) {
		t.Fatalf("expected allowed origin")
	}

	r2 := httptest.NewRequest(http.MethodGet, "/", nil)
	r2.Header.Set("Origin", "https://evil.com")
	if upgrader.CheckOrigin(r2) {
		t.Fatalf("expected disallowed origin")
	}
}

func TestSameSiteFromEnv(t *testing.T) {
	// Strict default
	_ = os.Unsetenv("HERMOD_COOKIE_SAMESITE")
	if got := sameSiteFromEnv(); got != http.SameSiteStrictMode {
		t.Fatalf("expected Strict default, got %v", got)
	}
	// Lax
	_ = os.Setenv("HERMOD_COOKIE_SAMESITE", "Lax")
	if got := sameSiteFromEnv(); got != http.SameSiteLaxMode {
		t.Fatalf("expected Lax, got %v", got)
	}
	// None
	_ = os.Setenv("HERMOD_COOKIE_SAMESITE", "None")
	if got := sameSiteFromEnv(); got != http.SameSiteNoneMode {
		t.Fatalf("expected None, got %v", got)
	}
	// Strict
	_ = os.Setenv("HERMOD_COOKIE_SAMESITE", "Strict")
	if got := sameSiteFromEnv(); got != http.SameSiteStrictMode {
		t.Fatalf("expected Strict, got %v", got)
	}
}

func TestSecurityHeadersCSP(t *testing.T) {
	// Save/restore env
	oldEnv := os.Getenv("HERMOD_ENV")
	oldCSP := os.Getenv("HERMOD_CSP")
	defer func() {
		_ = os.Setenv("HERMOD_ENV", oldEnv)
		_ = os.Setenv("HERMOD_CSP", oldCSP)
	}()

	_ = os.Setenv("HERMOD_ENV", "production")
	_ = os.Setenv("HERMOD_CSP", "")

	s := &Server{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	s.securityHeadersMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rr, req)
	got := rr.Header().Get("Content-Security-Policy")
	if got == "" || strings.Contains(got, "'unsafe-inline'") {
		t.Fatalf("expected CSP without 'unsafe-inline', got %q", got)
	}

	// Override
	_ = os.Setenv("HERMOD_CSP", "default-src 'none'")
	rr2 := httptest.NewRecorder()
	s.securityHeadersMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rr2, req)
	if got2 := rr2.Header().Get("Content-Security-Policy"); got2 != "default-src 'none'" {
		t.Fatalf("expected override CSP, got %q", got2)
	}
}
