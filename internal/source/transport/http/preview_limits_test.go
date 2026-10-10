package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/engine/registry"
	"github.com/gsoultan/hermod/internal/storage"
)

// 3.11 of the performance review: the sample endpoints decoded whatever body
// they were sent, and ProxyFetch read whatever its target sent back.

type sampleStorage struct {
	storage.Storage
	block bool
}

func (s *sampleStorage) GetSource(_ context.Context, id string) (storage.Source, error) {
	return storage.Source{ID: id, Name: id, Type: "postgres"}, nil
}

func (s *sampleStorage) UpdateSourceSample(ctx context.Context, _ string, _ string) error {
	if s.block {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

func postJSON(t *testing.T, handler http.HandlerFunc, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "src-1")
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

func overCap(t *testing.T, limit int64, shape func(pad string) any) []byte {
	t.Helper()
	body, err := json.Marshal(shape(strings.Repeat("x", int(limit)+1)))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return body
}

func TestStoreSourceSampleRejectsAnOversizedBody(t *testing.T) {
	h := &SourceHandler{Handler: &handlers.Handler{Storage: &sampleStorage{}}}
	body := overCap(t, handlers.PreviewMaxBodyBytes, func(pad string) any {
		return map[string]any{"sample": pad}
	})

	rec := postJSON(t, h.StoreSourceSample, "/api/sources/src-1/sample", body)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("StoreSourceSample with a %d-byte body returned %d, want 413: %.200s",
			len(body), rec.Code, rec.Body.String())
	}
}

// TestStoreSourceSampleHasADeadline: a storage write that never returns must
// not hold the request open indefinitely.
func TestStoreSourceSampleHasADeadline(t *testing.T) {
	prev := handlers.PreviewTimeout
	handlers.PreviewTimeout = 200 * time.Millisecond
	t.Cleanup(func() { handlers.PreviewTimeout = prev })

	h := &SourceHandler{Handler: &handlers.Handler{Storage: &sampleStorage{block: true}}}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- postJSON(t, h.StoreSourceSample, "/api/sources/src-1/sample", []byte(`{"sample":"{}"}`))
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("StoreSourceSample was still waiting on storage 10s in with a 200ms preview deadline")
	}
}

func TestSampleSourceTableRejectsAnOversizedBody(t *testing.T) {
	reg := registry.NewRegistry(nil)
	t.Cleanup(reg.Close)
	h := &SourceHandler{Handler: &handlers.Handler{Registry: reg}}
	body := overCap(t, handlers.PreviewMaxBodyBytes, func(pad string) any {
		return map[string]any{
			"source": map[string]any{"type": "postgres", "config": map[string]any{"pad": pad}},
			"table":  "orders",
		}
	})

	rec := postJSON(t, h.SampleSourceTable, "/api/sources/sample", body)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("SampleSourceTable with a %d-byte body returned %d, want 413: %.200s",
			len(body), rec.Code, rec.Body.String())
	}
}

func TestProxyFetchRejectsAnOversizedBody(t *testing.T) {
	h := &SourceHandler{Handler: &handlers.Handler{}}
	body := overCap(t, proxyFetchMaxRequestBytes, func(pad string) any {
		return map[string]any{"url": "http://example.invalid/", "method": "GET", "headers": map[string]string{"X-Pad": pad}}
	})

	rec := postJSON(t, h.ProxyFetch, "/api/proxy/fetch", body)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("ProxyFetch with a %d-byte body returned %d, want 413: %.200s",
			len(body), rec.Code, rec.Body.String())
	}
}

// TestProxyFetchCapsTheResponseItReads: whatever the target sends was read into
// memory in full and echoed back, so one large response cost the server its
// size twice over.
func TestProxyFetchCapsTheResponseItReads(t *testing.T) {
	big := strings.Repeat("y", int(proxyFetchMaxResponseBytes)+1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(big))
	}))
	t.Cleanup(srv.Close)

	prev := proxyFetchClient
	proxyFetchClient = srv.Client()
	t.Cleanup(func() { proxyFetchClient = prev })

	h := &SourceHandler{Handler: &handlers.Handler{}}
	body, _ := json.Marshal(map[string]any{"url": srv.URL, "method": "GET"})
	rec := postJSON(t, h.ProxyFetch, "/api/proxy/fetch", body)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("ProxyFetch of a %d-byte response returned %d, want 502", len(big), rec.Code)
	}
	if int64(rec.Body.Len()) > proxyFetchMaxResponseBytes {
		t.Errorf("ProxyFetch echoed %d bytes, more than its %d-byte cap", rec.Body.Len(), proxyFetchMaxResponseBytes)
	}
}
