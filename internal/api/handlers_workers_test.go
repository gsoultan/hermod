package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/user/hermod/internal/storage"
)

// fakeWorkerStorage embeds mockStorage and provides in-memory workers behavior
type fakeWorkerStorage struct {
	mockStorage
	workers map[string]storage.Worker
}

func (f *fakeWorkerStorage) ListWorkers(_ context.Context, _ storage.CommonFilter) ([]storage.Worker, int, error) {
	out := make([]storage.Worker, 0, len(f.workers))
	for _, w := range f.workers {
		out = append(out, w)
	}
	return out, len(out), nil
}

func (f *fakeWorkerStorage) GetWorker(_ context.Context, id string) (storage.Worker, error) {
	if w, ok := f.workers[id]; ok {
		return w, nil
	}
	return storage.Worker{}, storage.ErrNotFound
}

func (f *fakeWorkerStorage) CreateWorker(_ context.Context, w storage.Worker) error {
	if f.workers == nil {
		f.workers = map[string]storage.Worker{}
	}
	f.workers[w.ID] = w
	return nil
}

func (f *fakeWorkerStorage) UpdateWorker(_ context.Context, w storage.Worker) error {
	if f.workers == nil {
		f.workers = map[string]storage.Worker{}
	}
	f.workers[w.ID] = w
	return nil
}

func TestListWorkersHidesToken(t *testing.T) {
	s := &Server{storage: &fakeWorkerStorage{workers: map[string]storage.Worker{
		"w1": {ID: "w1", Name: "worker-1", Token: "secret", Host: "h", Port: 1},
	}}}

	req := httptest.NewRequest("GET", "/api/workers", nil)
	rec := httptest.NewRecorder()
	s.listWorkers(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	var res struct {
		Data  []storage.Worker `json:"data"`
		Total int              `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if len(res.Data) != 1 {
		t.Fatalf("expected 1 worker, got %d", len(res.Data))
	}
	if res.Data[0].Token != "" {
		t.Fatalf("expected empty token in list response, got %q", res.Data[0].Token)
	}
}

func TestGetWorkerHidesToken(t *testing.T) {
	now := time.Now()
	s := &Server{storage: &fakeWorkerStorage{workers: map[string]storage.Worker{
		"w1": {ID: "w1", Name: "worker-1", Token: "secret", LastSeen: &now},
	}}}
	req := httptest.NewRequest("GET", "/api/workers/w1", nil)
	req.SetPathValue("id", "w1")
	rec := httptest.NewRecorder()
	s.getWorker(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	var w storage.Worker
	if err := json.Unmarshal(rec.Body.Bytes(), &w); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if w.Token != "" {
		t.Fatalf("expected empty token in get response, got %q", w.Token)
	}
}

func TestUpdateWorkerPreservesToken(t *testing.T) {
	s := &Server{storage: &fakeWorkerStorage{workers: map[string]storage.Worker{
		"w1": {ID: "w1", Name: "old-name", Token: "secret"},
	}}}
	body := map[string]interface{}{"name": "new-name", "token": "evil"}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("PUT", "/api/workers/w1", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "w1")
	rec := httptest.NewRecorder()
	s.updateWorker(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	// Token must remain unchanged in storage
	st := s.storage.(*fakeWorkerStorage)
	if st.workers["w1"].Token != "secret" {
		t.Fatalf("expected token to be preserved, got %q", st.workers["w1"].Token)
	}
	// Response should also hide token
	var w storage.Worker
	if err := json.Unmarshal(rec.Body.Bytes(), &w); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if w.Token != "" {
		t.Fatalf("expected empty token in update response, got %q", w.Token)
	}
}
