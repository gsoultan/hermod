package http

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/engine/registry"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// manualRunTimeout bounds one manual run, AI calls and sink writes included.
const manualRunTimeout = 5 * time.Minute

// runRequest is the body of POST /api/workflows/{id}/run.
type runRequest struct {
	// Message is the payload the run's source node emits.
	Message map[string]any `json:"message"`
	// SourceNodeID picks the source node to start at; the first source node
	// when empty.
	SourceNodeID string `json:"source_node_id"`
}

// runResponse is what a run answers with.
type runResponse struct {
	WorkflowID string `json:"workflow_id"`
	registry.RunResult
}

// RunWorkflow runs the workflow once with the payload in the body and answers
// with the run id (its trace id), how it ended and every step.
func (h *ExecutionsHandler) RunWorkflow(w http.ResponseWriter, r *http.Request) {
	wf, ok := h.loadWorkflow(w, r)
	if !ok {
		return
	}
	var req runRequest
	if !h.DecodeJSONBody(w, r, handlers.PreviewMaxBodyBytes, &req, "Invalid run request: ") {
		return
	}
	if req.Message == nil {
		h.JsonError(w, `The run needs a "message" object to send through the workflow`, http.StatusBadRequest)
		return
	}

	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	message.PopulateFromMap(msg, req.Message)

	ctx, cancel := runContext(r)
	defer cancel()
	res, err := h.Registry.RunWorkflowOnce(ctx, wf, msg, req.SourceNodeID)
	if err != nil && res.RunID == "" {
		h.JsonError(w, "Failed to run workflow: "+err.Error(), http.StatusBadRequest)
		return
	}
	h.RecordAuditLog(r, "INFO", "Ran workflow "+wf.ID+" with a supplied payload (run "+res.RunID+")", "manual_run", wf.ID, "", "", nil)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(runResponse{WorkflowID: wf.ID, RunResult: res})
}
