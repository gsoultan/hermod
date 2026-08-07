package http

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/user/hermod/internal/api/handlers"
	"github.com/user/hermod/internal/storage"
)

// recordingLogStorage captures batches without a database.
type recordingLogStorage struct {
	storage.Storage
	mu       sync.Mutex
	batches  [][]storage.Log
	failNext error
}

func (s *recordingLogStorage) CreateLogs(_ context.Context, logs []storage.Log) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failNext != nil {
		err := s.failNext
		s.failNext = nil
		return err
	}
	s.batches = append(s.batches, logs)
	return nil
}

func (s *recordingLogStorage) received() []storage.Log {
	s.mu.Lock()
	defer s.mu.Unlock()
	var all []storage.Log
	for _, b := range s.batches {
		all = append(all, b...)
	}
	return all
}

// postBatch sends a batch to the endpoint with a request-scoped context.
func postBatch(t *testing.T, url string, body io.Reader) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, body)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func newBatchTestServer(t *testing.T, store *recordingLogStorage) *httptest.Server {
	t.Helper()
	h := NewLogHandler(&handlers.Handler{LogStorage: store})
	mux := http.NewServeMux()
	h.RegisterLogRoutes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// A remote worker runs the engines but has no database of its own, so its
// workflow logs have to travel to the platform to be seen in the UI at all.
// Shipping them one HTTP request at a time is not viable — the engine's logger
// batches fifty at a time — so the platform needs to accept a batch.
func TestCreateLogsBatchEndpoint(t *testing.T) {
	t.Run("a batch is accepted and stored", func(t *testing.T) {
		store := &recordingLogStorage{}
		srv := newBatchTestServer(t, store)

		batch := []storage.Log{
			{Timestamp: time.Now(), Level: "ERROR", Message: "Pipeline stalled", WorkflowID: "wf-1"},
			{Timestamp: time.Now(), Level: "INFO", Message: "Stalled workflow restarted", WorkflowID: "wf-1"},
		}
		body, _ := json.Marshal(batch)

		resp := postBatch(t, srv.URL+"/api/logs/batch", bytes.NewReader(body))

		if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %s, want 201/200", resp.Status)
		}
		got := store.received()
		if len(got) != len(batch) {
			t.Fatalf("stored %d logs, want %d", len(got), len(batch))
		}
		if got[0].Message != "Pipeline stalled" || got[0].WorkflowID != "wf-1" {
			t.Errorf("stored log = %+v, want the submitted one", got[0])
		}
	})

	t.Run("an empty batch is accepted without touching storage", func(t *testing.T) {
		store := &recordingLogStorage{}
		srv := newBatchTestServer(t, store)

		resp := postBatch(t, srv.URL+"/api/logs/batch", strings.NewReader("[]"))
		if resp.StatusCode >= 400 {
			t.Errorf("status = %s, want success for an empty batch", resp.Status)
		}
	})

	t.Run("malformed JSON is rejected, not stored", func(t *testing.T) {
		store := &recordingLogStorage{}
		srv := newBatchTestServer(t, store)

		resp := postBatch(t, srv.URL+"/api/logs/batch", strings.NewReader("{not json"))
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("status = %s, want 400", resp.Status)
		}
	})

	t.Run("an oversized body is refused rather than buffered", func(t *testing.T) {
		// Log shipping is an unauthenticated-ish, high-volume path from many
		// workers. Decoding an unbounded body would let one of them exhaust the
		// platform's memory.
		store := &recordingLogStorage{}
		srv := newBatchTestServer(t, store)

		huge := bytes.NewBuffer(nil)
		huge.WriteString(`[{"message":"`)
		huge.Write(bytes.Repeat([]byte("A"), maxLogBatchBytes+1024))
		huge.WriteString(`"}]`)

		resp := postBatch(t, srv.URL+"/api/logs/batch", huge)
		if resp.StatusCode < 400 {
			t.Errorf("status = %s, want a rejection for an oversized batch", resp.Status)
		}
		if n := len(store.received()); n != 0 {
			t.Errorf("stored %d logs from an oversized body", n)
		}
	})
}
