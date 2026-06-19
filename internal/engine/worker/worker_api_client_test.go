package worker

import (
	"net/http"
	"net/http/httptest"
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
