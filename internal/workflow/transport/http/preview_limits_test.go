package http

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/engine/registry"
	_ "github.com/gsoultan/hermod/pkg/comm/transformer/lookup"
)

// The editor's preview endpoints decode whatever body they are sent and then
// run the workflow or node against it with no deadline of their own. These pin
// 3.11 of the performance review: a size cap answered with 413, and a deadline
// that ends a preview stuck on a node that never answers.

// oversizedPreviewBody is a valid request one byte-ish past the cap, so the
// only thing wrong with it is its size.
func oversizedPreviewBody(t *testing.T, shape func(pad string) map[string]any) []byte {
	t.Helper()
	pad := strings.Repeat("x", int(handlers.PreviewMaxBodyBytes)+1)
	body, err := json.Marshal(shape(pad))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return body
}

func postPreview(t *testing.T, handler http.HandlerFunc, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

func TestTestWorkflowRejectsAnOversizedBody(t *testing.T) {
	h := &WorkflowHandler{Handler: &handlers.Handler{Registry: &registry.Registry{}}}
	body := oversizedPreviewBody(t, func(pad string) map[string]any {
		return map[string]any{
			"workflow": map[string]any{"nodes": []any{}},
			"message":  map[string]any{"pad": pad},
			"partial":  true,
		}
	})

	rec := postPreview(t, h.TestWorkflow, "/api/workflows/test", body)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("POST /api/workflows/test with a %d-byte body returned %d, want 413: %.200s",
			len(body), rec.Code, rec.Body.String())
	}
}

func TestTestTransformationRejectsAnOversizedBody(t *testing.T) {
	h := &WorkflowHandler{Handler: &handlers.Handler{Registry: &registry.Registry{}}}
	body := oversizedPreviewBody(t, func(pad string) map[string]any {
		return map[string]any{
			"transformation": map[string]any{"type": "set", "config": map[string]any{}},
			"message":        map[string]any{"pad": pad},
		}
	})

	rec := postPreview(t, h.TestTransformation, "/api/transformations/test", body)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("POST /api/transformations/test with a %d-byte body returned %d, want 413: %.200s",
			len(body), rec.Code, rec.Body.String())
	}
}

// TestTestTransformationHasADeadline: an api_lookup previewed against an
// endpoint that never answers held the request for the node's own timeout,
// which the operator sets and may set to hours.
func TestTestTransformationHasADeadline(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(func() {
		close(release)
		srv.Close()
	})

	prev := handlers.PreviewTimeout
	handlers.PreviewTimeout = 200 * time.Millisecond
	t.Cleanup(func() { handlers.PreviewTimeout = prev })

	h := &WorkflowHandler{Handler: &handlers.Handler{Registry: registry.NewRegistry(nil)}}
	t.Cleanup(h.Registry.Close)
	body, err := json.Marshal(map[string]any{
		"transformation": map[string]any{"type": "api_lookup", "config": map[string]any{
			"transType":   "api_lookup",
			"method":      "GET",
			"url":         srv.URL + "/slow",
			"targetField": "tier",
			"timeout":     "1h",
		}},
		"message": map[string]any{"id": 1},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- postPreview(t, h.TestTransformation, "/api/transformations/test", body) }()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("a transformation preview against an endpoint that never answers was still running " +
			"after 10s with a 200ms preview deadline; the preview has no deadline")
	}
}
