// Package http is the REST surface of the vhost AI budgets: reading a vhost's
// budget and this month's usage, setting the budget and the per-workflow caps,
// the kill switch, and the two routes a worker uses to have its model calls
// checked and counted by the control plane.
package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/gsoultan/hermod/internal/aibudget"
	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/llm"
)

// maxBudgetBody bounds a budget, which holds at most a few thousand prices
// and caps; maxWorkerBody bounds a worker's check or usage report.
const (
	maxBudgetBody = 512 << 10
	maxWorkerBody = 16 << 10
)

// Handler serves the AI budget routes.
type Handler struct {
	*handlers.Handler
}

// NewHandler wraps the shared API handler.
func NewHandler(h *handlers.Handler) *Handler {
	return &Handler{Handler: h}
}

// RegisterRoutes mounts the budget routes. Writes are behind EditorOnly, and
// access() then checks the vhost in the path, which a role wrapper cannot.
// The worker routes answer a worker's token only (see worker()).
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/vhosts/{vhost}/ai/budget", h.GetBudget)
	mux.Handle("PUT /api/vhosts/{vhost}/ai/budget", h.EditorOnly(h.PutBudget))
	mux.Handle("PUT /api/vhosts/{vhost}/ai/kill-switch", h.EditorOnly(h.PutKillSwitch))
	mux.HandleFunc("POST /api/worker/ai/check", h.WorkerCheck)
	mux.HandleFunc("POST /api/worker/ai/usage", h.WorkerUsage)
}

// service is the registry's budget when there is a registry, so a save
// applies at once to the calls this process makes; before setup there is
// none, and a service over the API's storage reads and writes the same rows.
func (h *Handler) service() *aibudget.Service {
	if h.Registry != nil {
		return h.Registry.AIBudget()
	}
	return aibudget.NewService(func() any { return h.Storage }, nil, nil)
}

// access answers who is asking about which vhost, and refuses anyone who may
// not. Reading a vhost's budget and usage needs any role on the vhost;
// changing it needs Editor.
func (h *Handler) access(w http.ResponseWriter, r *http.Request, write bool) (string, *storage.User, bool) {
	user, _ := r.Context().Value(handlers.UserContextKey).(*storage.User)
	if user == nil {
		h.JsonError(w, "Forbidden", http.StatusForbidden)
		return "", nil, false
	}
	if write && user.Role != storage.RoleAdministrator && user.Role != storage.RoleEditor {
		h.JsonError(w, "Forbidden: this needs the Editor role", http.StatusForbidden)
		return "", nil, false
	}
	vhost := r.PathValue("vhost")
	if vhost == "" || vhost == "all" {
		h.JsonError(w, "an AI budget belongs to one vhost: name it", http.StatusBadRequest)
		return "", nil, false
	}
	if user.Role != storage.RoleAdministrator && !h.HasVHostAccess(vhost, user.VHosts) {
		h.JsonError(w, "Forbidden: you do not have access to this vhost", http.StatusForbidden)
		return "", nil, false
	}
	return vhost, user, true
}

func (h *Handler) fail(w http.ResponseWriter, err error, what string) {
	if errors.Is(err, storage.ErrAIBudgetsUnsupported) {
		h.JsonError(w, err.Error(), http.StatusNotImplemented)
		return
	}
	h.JsonError(w, what, http.StatusInternalServerError)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// GetBudget returns the vhost's budget and this month's usage, the vhost's
// total and each workflow's.
func (h *Handler) GetBudget(w http.ResponseWriter, r *http.Request) {
	vhost, _, ok := h.access(w, r, false)
	if !ok {
		return
	}
	rep, err := h.service().Report(r.Context(), vhost)
	if err != nil {
		h.fail(w, err, "Failed to read the AI budget")
		return
	}
	writeJSON(w, rep)
}

// budgetRequest is what a client may set. The vhost and the bookkeeping come
// from the path and the server, never the body.
type budgetRequest struct {
	Disabled      bool                    `json:"disabled"`
	MonthlyTokens int64                   `json:"monthly_tokens"`
	MonthlyCost   float64                 `json:"monthly_cost"`
	Currency      string                  `json:"currency"`
	Prices        []storage.AIModelPrice  `json:"prices"`
	Workflows     []storage.AIWorkflowCap `json:"workflows"`
}

// PutBudget replaces the vhost's budget, kill switch included.
func (h *Handler) PutBudget(w http.ResponseWriter, r *http.Request) {
	vhost, user, ok := h.access(w, r, true)
	if !ok {
		return
	}
	var req budgetRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBudgetBody)).Decode(&req); err != nil {
		h.JsonError(w, "the request body must be a JSON AI budget", http.StatusBadRequest)
		return
	}
	b := storage.AIBudget{
		VHost: vhost, Disabled: req.Disabled, MonthlyTokens: req.MonthlyTokens, MonthlyCost: req.MonthlyCost,
		Currency: strings.TrimSpace(req.Currency), Prices: req.Prices, Workflows: req.Workflows, UpdatedBy: user.Username,
	}
	h.save(w, r, b, "Saved the AI budget of vhost "+vhost)
}

// PutKillSwitch turns the vhost's AI calls off or back on, keeping its limits.
func (h *Handler) PutKillSwitch(w http.ResponseWriter, r *http.Request) {
	vhost, user, ok := h.access(w, r, true)
	if !ok {
		return
	}
	var req struct {
		Disabled *bool `json:"disabled"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxWorkerBody)).Decode(&req); err != nil || req.Disabled == nil {
		h.JsonError(w, `the request body must be {"disabled": true} or {"disabled": false}`, http.StatusBadRequest)
		return
	}
	rep, err := h.service().Report(r.Context(), vhost)
	if err != nil {
		h.fail(w, err, "Failed to read the AI budget")
		return
	}
	b := rep.Budget
	b.Disabled, b.UpdatedBy = *req.Disabled, user.Username
	msg := "Switched AI calls back on in vhost " + vhost
	if b.Disabled {
		msg = "Switched AI calls off in vhost " + vhost
	}
	h.save(w, r, b, msg)
}

func (h *Handler) save(w http.ResponseWriter, r *http.Request, b storage.AIBudget, msg string) {
	if err := storage.ValidateAIBudget(b); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	svc := h.service()
	if err := svc.Save(r.Context(), b); err != nil {
		h.fail(w, err, "Failed to save the AI budget")
		return
	}
	h.RecordAuditLog(r, "INFO", msg, "update", b.VHost, "vhost", "", map[string]any{
		"ai_budget": map[string]any{
			"disabled": b.Disabled, "monthly_tokens": b.MonthlyTokens, "monthly_cost": b.MonthlyCost,
			"currency": b.Currency, "prices": len(b.Prices), "workflow_caps": b.Workflows,
		},
	})
	rep, err := svc.Report(r.Context(), b.VHost)
	if err != nil {
		h.fail(w, err, "Failed to read the AI budget")
		return
	}
	writeJSON(w, rep)
}

// worker refuses anyone but a worker. A worker has no database, so it asks
// the control plane to check and count its calls; nothing a person can do
// with these routes is not done better through the others.
func (h *Handler) worker(w http.ResponseWriter, r *http.Request) bool {
	user, _ := r.Context().Value(handlers.UserContextKey).(*storage.User)
	if user == nil || !strings.HasPrefix(user.ID, "worker:") {
		h.JsonError(w, "Forbidden: only a worker uses this route", http.StatusForbidden)
		return false
	}
	return true
}

// checkResponse is a worker's answer: allowed, or the BudgetError's fields.
type checkResponse struct {
	Allowed    bool   `json:"allowed"`
	Limit      string `json:"limit,omitempty"`
	VHost      string `json:"vhost,omitempty"`
	WorkflowID string `json:"workflow_id,omitempty"`
	Used       int64  `json:"used,omitempty"`
	Max        int64  `json:"max,omitempty"`
	Message    string `json:"message,omitempty"`
}

type scopeRequest struct {
	VHost      string `json:"vhost"`
	WorkflowID string `json:"workflow_id"`
}

// WorkerCheck answers whether a worker's call may go ahead.
func (h *Handler) WorkerCheck(w http.ResponseWriter, r *http.Request) {
	if !h.worker(w, r) {
		return
	}
	var req scopeRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxWorkerBody)).Decode(&req); err != nil {
		h.JsonError(w, "the request body must be JSON", http.StatusBadRequest)
		return
	}
	err := h.service().Check(r.Context(), req.VHost, req.WorkflowID)
	var be *llm.BudgetError
	switch {
	case err == nil:
		writeJSON(w, checkResponse{Allowed: true})
	case errors.As(err, &be):
		writeJSON(w, checkResponse{Limit: string(be.Limit), VHost: be.VHost, WorkflowID: be.WorkflowID,
			Used: be.Used, Max: be.Max, Message: be.Error()})
	default:
		h.JsonError(w, "Failed to check the AI budget", http.StatusInternalServerError)
	}
}

type usageRequest struct {
	scopeRequest
	Provider     string `json:"provider"`
	Model        string `json:"model"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
}

// WorkerUsage counts a worker's finished call.
func (h *Handler) WorkerUsage(w http.ResponseWriter, r *http.Request) {
	if !h.worker(w, r) {
		return
	}
	var req usageRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxWorkerBody)).Decode(&req); err != nil {
		h.JsonError(w, "the request body must be JSON", http.StatusBadRequest)
		return
	}
	if req.InputTokens < 0 || req.OutputTokens < 0 {
		h.JsonError(w, "token counts must not be negative", http.StatusBadRequest)
		return
	}
	h.service().RecordUsage(r.Context(), req.VHost, req.WorkflowID, llm.CallRecord{
		Provider: req.Provider, Model: req.Model,
		Usage: llm.Usage{InputTokens: req.InputTokens, OutputTokens: req.OutputTokens},
	})
	w.WriteHeader(http.StatusNoContent)
}
