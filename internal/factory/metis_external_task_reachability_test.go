package factory

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
)

// TestMetisExternalTaskIsReachableFromStoredConfiguration runs the external-task
// integration the way an operator configures it: a stored source record, built
// through the factory, against an engine speaking the real wire contract.
//
// The unit tests build the source by calling its constructor, so they cannot
// see the seam that actually breaks — a config key the form writes under one
// name and the factory reads under another, or a decorator that does not
// forward Ack. This starts where the operator starts and asserts the outcome
// the process sees: the task completed, carrying what the pipeline produced.
func TestMetisExternalTaskIsReachableFromStoredConfiguration(t *testing.T) {
	var (
		mu          sync.Mutex
		completions []map[string]any
		fetched     bool
	)
	done := make(chan struct{})
	var once sync.Once

	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{}
		if r.Body != nil {
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &body)
		}
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.URL.Path == "/api/v1/external-tasks/fetch-and-lock":
			mu.Lock()
			already := fetched
			fetched = true
			mu.Unlock()
			if already {
				_, _ = w.Write([]byte(`{"tasks":[]}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"tasks": []map[string]any{{
				"id":               "task-1",
				"topic":            "reverse-charge",
				"retries":          2,
				"lock_expiration":  time.Now().Add(time.Minute).Format(time.RFC3339Nano),
				"node":             map[string]any{"id": "charge"},
				"process_instance": map[string]any{"id": "inst-1"},
				"variables":        map[string]any{"amount": 500.0, "card_number": "4111111111111111"},
			}}})

		case strings.HasSuffix(r.URL.Path, "/complete"):
			mu.Lock()
			completions = append(completions, body)
			mu.Unlock()
			once.Do(func() { close(done) })
			_, _ = w.Write([]byte(`{}`))

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer engine.Close()

	// Exactly what the source form stores: string keys, string values.
	src, err := CreateSource(SourceConfig{
		ID:   "src-metis-task",
		Type: "metis_task",
		Config: hermod.StringMap{
			"base_url":        engine.URL,
			"token":           "tok",
			"topic":           "reverse-charge",
			"worker_id":       "hermod-reachability",
			"max_tasks":       "1",
			"lock_duration":   "60s",
			"poll_interval":   "10ms",
			"variable_fields": "reversed, reference",
		},
	})
	if err != nil {
		t.Fatalf("CreateSource: %v", err)
	}
	t.Cleanup(func() { _ = src.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	msg, err := src.Read(ctx)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if msg == nil {
		t.Fatal("the factory-built source read no task")
	}
	if got := msg.Data()["amount"]; got != 500.0 {
		t.Fatalf("the step's variables did not survive the factory: want amount=500, got %v", got)
	}

	// Stand in for the pipeline's work.
	msg.SetData("reversed", true)
	msg.SetData("reference", "RV-77")

	if err := src.Ack(ctx, msg); err != nil {
		t.Fatalf("Ack: %v", err)
	}

	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("the completion never reached the engine")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(completions) != 1 {
		t.Fatalf("want one completion, got %d", len(completions))
	}
	got := completions[0]
	if got["worker_id"] != "hermod-reachability" {
		t.Errorf("worker_id from stored config did not reach the engine: %v", got["worker_id"])
	}
	vars, _ := got["variables"].(map[string]any)
	if vars["reversed"] != true || vars["reference"] != "RV-77" {
		t.Errorf("the pipeline's output never reached the process: %v", vars)
	}
	// variable_fields is stored as one comma-separated string; if the factory
	// does not split it, nothing is selected and the card number goes back into
	// the instance.
	if _, leaked := vars["card_number"]; leaked {
		t.Errorf("variable_fields did not survive the factory: %v", vars)
	}
}
