package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/api/handlers"
)

// The widget script is served to anyone, so it must work under a strict
// Content-Security-Policy on the page that embeds it and must never write the
// model's answer into the page as HTML.
func TestTheWidgetScriptIsServedAndCSPFriendly(t *testing.T) {
	base := &handlers.Handler{}
	mux := http.NewServeMux()
	NewChatHandler(base).RegisterChatRoutes(mux)

	rec := httptest.NewRecorder()
	base.AuthMiddleware(mux).ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/chat/widget.js", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") {
		t.Errorf("Content-Type = %q", ct)
	}
	js := rec.Body.String()
	for _, want := range []string{"widget_key", "data-endpoint", "conversation_id", "textContent"} {
		if !strings.Contains(js, want) {
			t.Errorf("the script does not mention %q", want)
		}
	}
	// eval and Function need 'unsafe-eval'; style attributes and <style>
	// elements need 'unsafe-inline'; innerHTML would render the answer as HTML.
	for _, banned := range []string{"eval(", "new Function", "innerHTML", "setAttribute('style'", "createElement('style')", "credentials: 'include'"} {
		if strings.Contains(js, banned) {
			t.Errorf("the script uses %q", banned)
		}
	}
}
