package worker

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gsoultan/Hermod/internal/storage"
)

type logSink struct {
	mu         sync.Mutex
	batches    int
	singles    int
	logs       []storage.Log
	batch404   bool
	batchFails int
}

func (s *logSink) handler(t *testing.T) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/logs/batch", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.batch404 {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if s.batchFails > 0 {
			s.batchFails--
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		var logs []storage.Log
		if err := json.NewDecoder(r.Body).Decode(&logs); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		s.batches++
		s.logs = append(s.logs, logs...)
		w.WriteHeader(http.StatusCreated)
	})
	mux.HandleFunc("POST /api/logs", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		var l storage.Log
		if err := json.NewDecoder(r.Body).Decode(&l); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		s.singles++
		s.logs = append(s.logs, l)
		w.WriteHeader(http.StatusCreated)
	})
	return mux
}

func (s *logSink) counts() (batches, singles, logs int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.batches, s.singles, len(s.logs)
}

// In platform-worker mode the registry is backed by the API client, and the
// adapter's CreateLogs was a `return nil` stub. Every workflow log a remote
// worker produced was accepted and thrown away, so the platform's log view was
// empty for exactly the deployments where reading a worker's console is hardest.
func TestWorkerShipsLogBatchesToThePlatform(t *testing.T) {
	t.Run("a batch goes in one request", func(t *testing.T) {
		sink := &logSink{}
		srv := httptest.NewServer(sink.handler(t))
		defer srv.Close()

		store := NewAPIStorage(NewWorkerAPIClient(srv.URL, "token"))
		logs := []storage.Log{
			{Timestamp: time.Now(), Level: "ERROR", Message: "Pipeline stalled", WorkflowID: "wf-1"},
			{Timestamp: time.Now(), Level: "INFO", Message: "restarted", WorkflowID: "wf-1"},
		}

		if err := store.CreateLogs(t.Context(), logs); err != nil {
			t.Fatalf("CreateLogs: %v", err)
		}

		batches, singles, got := sink.counts()
		if got != len(logs) {
			t.Fatalf("platform received %d logs, want %d: worker logs are still being discarded", got, len(logs))
		}
		if batches != 1 {
			t.Errorf("sent %d batch requests, want 1", batches)
		}
		if singles != 0 {
			t.Errorf("fell back to %d per-log requests when the batch endpoint was available", singles)
		}
	})

	t.Run("an older platform without the batch route still receives the logs", func(t *testing.T) {
		// Workers and the platform are upgraded separately; a worker that only
		// knows how to batch would go silent against a platform that predates
		// the route.
		sink := &logSink{batch404: true}
		srv := httptest.NewServer(sink.handler(t))
		defer srv.Close()

		store := NewAPIStorage(NewWorkerAPIClient(srv.URL, "token"))
		logs := []storage.Log{
			{Timestamp: time.Now(), Level: "ERROR", Message: "Pipeline stalled", WorkflowID: "wf-1"},
			{Timestamp: time.Now(), Level: "WARN", Message: "slow sink", WorkflowID: "wf-1"},
		}

		if err := store.CreateLogs(t.Context(), logs); err != nil {
			t.Fatalf("CreateLogs against an older platform: %v", err)
		}

		_, singles, got := sink.counts()
		if got != len(logs) {
			t.Fatalf("platform received %d logs, want %d", got, len(logs))
		}
		if singles != len(logs) {
			t.Errorf("fell back with %d single requests, want %d", singles, len(logs))
		}
	})

	t.Run("an empty batch makes no request", func(t *testing.T) {
		var hits atomic.Int64
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			w.WriteHeader(http.StatusCreated)
		}))
		defer srv.Close()

		store := NewAPIStorage(NewWorkerAPIClient(srv.URL, "token"))
		if err := store.CreateLogs(t.Context(), nil); err != nil {
			t.Fatalf("CreateLogs(nil): %v", err)
		}
		if n := hits.Load(); n != 0 {
			t.Errorf("made %d requests for an empty batch", n)
		}
	})

	t.Run("a platform error is reported, not swallowed", func(t *testing.T) {
		// The caller throttles and reports these on the process log. Returning
		// nil here would restore the original defect: logs vanish silently.
		sink := &logSink{batchFails: 1}
		srv := httptest.NewServer(sink.handler(t))
		defer srv.Close()

		store := NewAPIStorage(NewWorkerAPIClient(srv.URL, "token"))
		err := store.CreateLogs(t.Context(), []storage.Log{{Message: "x", WorkflowID: "wf-1"}})
		if err == nil {
			t.Error("a failed shipment reported success, so the operator never learns the logs were lost")
		}
	})
}
