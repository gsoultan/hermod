package worker

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gsoultan/hermod/internal/aibudget"
	"github.com/gsoultan/hermod/pkg/llm"
)

// A worker has no database, so its AI calls are checked and counted by the
// control plane. Its storage is what the budget service finds, so it has to
// be an aibudget.Remote, or a worker would enforce nothing at all.
func TestWorkerStorageChecksAIBudgetThroughTheAPI(t *testing.T) {
	var usage map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Worker-Token") != "worker-token" {
			http.Error(w, "no token", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/worker/ai/check":
			var req map[string]string
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req["vhost"] == "spent" {
				_, _ = w.Write([]byte(`{"allowed":false,"limit":"workflow_tokens","vhost":"spent","workflow_id":"wf-1","used":12,"max":10,"message":"over"}`))
				return
			}
			_, _ = w.Write([]byte(`{"allowed":true}`))
		case "/api/worker/ai/usage":
			_ = json.NewDecoder(r.Body).Decode(&usage)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	remote, ok := NewAPIStorage(NewWorkerAPIClient(srv.URL, "worker-token")).(aibudget.Remote)
	if !ok {
		t.Fatal("the worker's API storage does not implement aibudget.Remote")
	}
	if err := remote.CheckAIBudget(t.Context(), "fine", "wf-1"); err != nil {
		t.Fatalf("allowed check: %v", err)
	}
	err := remote.CheckAIBudget(t.Context(), "spent", "wf-1")
	var be *llm.BudgetError
	if !errors.As(err, &be) || be.Limit != llm.LimitWorkflowTokens || be.Used != 12 || be.Max != 10 || be.WorkflowID != "wf-1" {
		t.Fatalf("refused check: err = %v", err)
	}

	rec := llm.CallRecord{Provider: "openai", Model: "m", Usage: llm.Usage{InputTokens: 3, OutputTokens: 4}}
	if err := remote.RecordAIUsage(t.Context(), "fine", "wf-1", rec); err != nil {
		t.Fatalf("RecordAIUsage: %v", err)
	}
	if usage["vhost"] != "fine" || usage["workflow_id"] != "wf-1" || usage["model"] != "m" || usage["output_tokens"] != float64(4) {
		t.Fatalf("usage sent = %v", usage)
	}
}

// A control plane that cannot answer refuses the call: budgets fail closed.
func TestWorkerAIBudgetFailsClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	remote := NewAPIStorage(NewWorkerAPIClient(srv.URL, "t")).(aibudget.Remote)

	err := remote.CheckAIBudget(t.Context(), "v", "")
	var be *llm.BudgetError
	if !errors.As(err, &be) || be.Limit != llm.LimitUnavailable {
		t.Fatalf("err = %v, want LimitUnavailable", err)
	}
	if err := remote.RecordAIUsage(t.Context(), "v", "", llm.CallRecord{}); err == nil {
		t.Fatal("a failed usage report was not reported")
	}
}
