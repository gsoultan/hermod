package worker

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestWorkerAPIClientDoRequestNilBody ensures that requests without a body
// (body == nil) do not panic. Previously bodyReader was typed as
// *bytes.Reader, so a typed-nil pointer was passed to
// http.NewRequestWithContext as a non-nil io.Reader, causing a nil pointer
// dereference in bytes.(*Reader).Len.
func TestWorkerAPIClientDoRequestNilBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewWorkerAPIClient(srv.URL, "")

	tests := []struct {
		name   string
		method string
		path   string
		body   any
	}{
		{"GET nil body", http.MethodGet, "/api/workflows", nil},
		{"DELETE nil body", http.MethodDelete, "/api/workers/abc", nil},
		{"POST with body", http.MethodPost, "/api/logs", map[string]string{"msg": "hi"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := c.doRequest(t.Context(), tc.method, tc.path, tc.body)
			if err != nil {
				t.Fatalf("%s: unexpected error: %v", tc.name, err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("%s: unexpected status: %d", tc.name, resp.StatusCode)
			}
		})
	}
}

// TestWorkerAPIClientPing verifies the startup connectivity probe and its
// classification of platform-url misconfigurations.
func TestWorkerAPIClientPing(t *testing.T) {
	// Healthy origin: Ping succeeds.
	t.Run("reachable", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		if err := NewWorkerAPIClient(srv.URL, "").Ping(t.Context()); err != nil {
			t.Fatalf("expected reachable origin to ping cleanly, got: %v", err)
		}
	})

	// https platform-url against a plaintext-HTTP origin: the classic
	// "server gave HTTP response to HTTPS client" mismatch must be flagged as a
	// deterministic config error so the worker fails fast.
	t.Run("https against http origin", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		httpsURL := "https://" + strings.TrimPrefix(srv.URL, "http://")
		err := NewWorkerAPIClient(httpsURL, "").Ping(t.Context())
		if err == nil {
			t.Fatal("expected scheme mismatch error, got nil")
		}
		if !errors.Is(err, ErrPlatformConfig) {
			t.Fatalf("expected ErrPlatformConfig, got: %v", err)
		}
	})

	// Empty / malformed platform-url is a config error, not a transient failure.
	t.Run("invalid url", func(t *testing.T) {
		for _, bad := range []string{"", "not-a-url", "ftp://host:1"} {
			err := NewWorkerAPIClient(bad, "").Ping(t.Context())
			if !errors.Is(err, ErrPlatformConfig) {
				t.Fatalf("platform-url %q: expected ErrPlatformConfig, got: %v", bad, err)
			}
		}
	})
}
