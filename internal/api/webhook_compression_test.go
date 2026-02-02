package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/user/hermod/internal/engine"
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/compression"
	"github.com/user/hermod/pkg/source/webhook"
)

type mockWebhookStorage struct {
	mockStorage
}

func (m *mockWebhookStorage) ListSources(ctx context.Context, filter storage.CommonFilter) ([]storage.Source, int, error) {
	return []storage.Source{
		{
			ID:   "test-webhook",
			Type: "webhook",
			Config: map[string]string{
				"path": "/api/webhooks/test",
			},
		},
	}, 1, nil
}

func TestWebhookDecompression(t *testing.T) {
	webhook.Register("/api/webhooks/test")
	defer webhook.Unregister("/api/webhooks/test")

	registry := engine.NewRegistry(nil)
	server := NewServer(registry, &mockWebhookStorage{}, nil, nil)
	handler := server.Routes()

	testData := "this is some test data for webhook decompression. It should be large enough."
	for i := 0; i < 20; i++ {
		testData += " extra data for compression"
	}

	algorithms := []compression.Algorithm{compression.LZ4, compression.Snappy, compression.Zstd}

	for _, algo := range algorithms {
		t.Run(string(algo), func(t *testing.T) {
			comp, _ := compression.NewCompressor(algo)
			compressed, err := comp.Compress([]byte(testData))
			if err != nil {
				t.Fatalf("failed to compress: %v", err)
			}

			req, _ := http.NewRequest("POST", "/api/webhooks/test", bytes.NewReader(compressed))
			req.Header.Set("Content-Encoding", string(algo))

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != http.StatusAccepted && rr.Code != http.StatusOK {
				t.Errorf("expected status 202 or 200, got %v. Body: %s", rr.Code, rr.Body.String())
			}

			// Note: verifying the actual dispatched message would require deeper mocking of the webhook registry,
			// but at least we've verified it doesn't return 400 Bad Request (Decompression failure).
		})
	}
}
