package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gsoultan/hermod/internal/executions"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

const (
	// defaultListLimit and maxListLimit bound a page of runs. Each run on the
	// page is one trace read, so the page size is the number of reads.
	defaultListLimit = 20
	maxListLimit     = 50
)

// listResponse is GET /api/workflows/{id}/executions.
type listResponse struct {
	Executions []executions.Execution `json:"executions"`
	// NextBefore is the cursor for the next (older) page: pass it as
	// ?before=. Empty when this page is the last.
	NextBefore string `json:"next_before,omitempty"`
}

// replayResponse is a replay's run, naming the run it replayed.
type replayResponse struct {
	runResponse
	ReplayOf string `json:"replay_of"`
}

// ListExecutions lists a workflow's recent runs, newest first, with each run's
// status, duration, steps and AI usage. ?limit= (default 20, at most 50) and
// ?before= (an RFC 3339 cursor from next_before) page through them.
func (h *ExecutionsHandler) ListExecutions(w http.ResponseWriter, r *http.Request) {
	wf, ok := h.loadWorkflow(w, r)
	if !ok {
		return
	}
	filter := storage.TraceFilter{Limit: defaultListLimit}
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 {
		filter.Limit = min(v, maxListLimit)
	}
	if b := r.URL.Query().Get("before"); b != "" {
		ts, err := time.Parse(time.RFC3339Nano, b)
		if err != nil {
			h.JsonError(w, "before must be an RFC 3339 timestamp", http.StatusBadRequest)
			return
		}
		filter.Before = ts
	}

	traces, err := h.LogStorage.ListMessageTraces(r.Context(), wf.ID, filter)
	if err != nil {
		h.JsonError(w, "Failed to list runs", http.StatusInternalServerError)
		return
	}
	resp := listResponse{Executions: make([]executions.Execution, 0, len(traces))}
	for _, summary := range traces {
		full, err := h.LogStorage.GetMessageTrace(r.Context(), wf.ID, summary.MessageID)
		if err != nil {
			// Swept by retention between the two reads: not a run any more.
			continue
		}
		resp.Executions = append(resp.Executions, executions.FromTrace(wf, full, false))
	}
	if len(traces) == filter.Limit {
		resp.NextBefore = traces[len(traces)-1].CreatedAt.Format(time.RFC3339Nano)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// GetExecution returns one run with every step's output.
func (h *ExecutionsHandler) GetExecution(w http.ResponseWriter, r *http.Request) {
	wf, ok := h.loadWorkflow(w, r)
	if !ok {
		return
	}
	trace, ok := h.loadTrace(w, r, wf.ID)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(executions.FromTrace(wf, trace, true))
}

// ReplayExecution runs a past run's input through the workflow again, from
// the same source node, as a new run.
func (h *ExecutionsHandler) ReplayExecution(w http.ResponseWriter, r *http.Request) {
	wf, ok := h.loadWorkflow(w, r)
	if !ok {
		return
	}
	trace, ok := h.loadTrace(w, r, wf.ID)
	if !ok {
		return
	}
	input, sourceNodeID, found := executions.Input(wf, trace)
	if !found {
		h.JsonError(w, "This run's trace has no input recorded at a source node, so it cannot be replayed", http.StatusConflict)
		return
	}

	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	message.PopulateFromMap(msg, input)

	ctx, cancel := runContext(r)
	defer cancel()
	res, err := h.Registry.RunWorkflowOnce(ctx, wf, msg, sourceNodeID)
	if err != nil && res.RunID == "" {
		h.JsonError(w, "Failed to replay run: "+err.Error(), http.StatusBadRequest)
		return
	}
	h.RecordAuditLog(r, "INFO", "Replayed run "+trace.MessageID+" of workflow "+wf.ID+" as run "+res.RunID, "replay_run", wf.ID, "", "", nil)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(replayResponse{runResponse: runResponse{WorkflowID: wf.ID, RunResult: res}, ReplayOf: trace.MessageID})
}

func (h *ExecutionsHandler) loadTrace(w http.ResponseWriter, r *http.Request, workflowID string) (storage.MessageTrace, bool) {
	trace, err := h.LogStorage.GetMessageTrace(r.Context(), workflowID, r.PathValue("run_id"))
	switch {
	case errors.Is(err, storage.ErrNotFound) || (err == nil && len(trace.Steps) == 0):
		h.JsonError(w, "Run not found", http.StatusNotFound)
		return storage.MessageTrace{}, false
	case err != nil:
		h.JsonError(w, "Failed to load run", http.StatusInternalServerError)
		return storage.MessageTrace{}, false
	}
	if trace.MessageID == "" {
		trace.MessageID = r.PathValue("run_id")
	}
	if trace.WorkflowID == "" {
		trace.WorkflowID = workflowID
	}
	return trace, true
}
