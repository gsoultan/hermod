// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

// Package http serves the self-healing proposals of a workflow.
package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/gsoultan/hermod/internal/advisor"
	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/selfheal"
	"github.com/gsoultan/hermod/internal/storage"
	workflowhttp "github.com/gsoultan/hermod/internal/workflow/transport/http"
)

// ProposalHandler lists, approves and rejects proposals.
type ProposalHandler struct {
	*handlers.Handler
	svc *selfheal.Service
	// insights replaces the registry's advisor; tests set it.
	insights insights
}

// insights is what the handler reads from the performance advisor.
type insights interface {
	WorkflowInsights(ctx context.Context, id string) advisor.Report
	WorkflowSummarySince(ctx context.Context, id string, since time.Time) (advisor.Summary, bool)
}

// advisor is the advisor in force, or nil when there is no engine registry.
func (h *ProposalHandler) advisor() insights {
	if h.insights != nil {
		return h.insights
	}
	if h.Registry != nil {
		return h.Registry
	}
	return nil
}

// NewProposalHandler builds the handler. Approval applies through the
// workflow handler's update path.
func NewProposalHandler(h *handlers.Handler) *ProposalHandler {
	return &ProposalHandler{
		Handler: h,
		svc:     selfheal.NewService(liveStore{h}, workflowhttp.NewWorkflowHandler(h)),
	}
}

// liveStore is selfheal.Store over whatever storage the handler holds now.
// First-run setup and a database switch replace h.Storage after the handler
// is built, so capturing it once would leave the service on a nil or closed
// store.
type liveStore struct{ h *handlers.Handler }

func (l liveStore) st() (storage.Storage, error) {
	if l.h.Storage == nil {
		return nil, errors.New("storage is not configured yet")
	}
	return l.h.Storage, nil
}

func (l liveStore) GetSetting(ctx context.Context, key string) (string, error) {
	st, err := l.st()
	if err != nil {
		return "", err
	}
	return st.GetSetting(ctx, key)
}

func (l liveStore) SaveSetting(ctx context.Context, key, value string) error {
	st, err := l.st()
	if err != nil {
		return err
	}
	return st.SaveSetting(ctx, key, value)
}

func (l liveStore) GetWorkflow(ctx context.Context, id string) (storage.Workflow, error) {
	st, err := l.st()
	if err != nil {
		return storage.Workflow{}, err
	}
	return st.GetWorkflow(ctx, id)
}

// Service is the proposal service the handler uses.
func (h *ProposalHandler) Service() *selfheal.Service { return h.svc }

// RegisterProposalRoutes mounts the API. Reading needs a Viewer; deciding
// changes the workflow and needs an Editor.
func (h *ProposalHandler) RegisterProposalRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/workflows/{id}/proposals", h.RbacMiddleware(storage.RoleViewer)(http.HandlerFunc(h.List)))
	mux.Handle("POST /api/workflows/{id}/proposals", h.EditorOnly(h.ProposeFix))
	mux.Handle("POST /api/workflows/{id}/proposals/{pid}/approve", h.EditorOnly(h.Approve))
	mux.Handle("POST /api/workflows/{id}/proposals/{pid}/reject", h.EditorOnly(h.Reject))
}

// workflow answers whether the caller may see the workflow, having written
// the refusal when not.
func (h *ProposalHandler) workflow(w http.ResponseWriter, r *http.Request) (storage.Workflow, bool) {
	wf, err := h.Storage.GetWorkflow(r.Context(), r.PathValue("id"))
	switch {
	case errors.Is(err, storage.ErrNotFound):
		h.JsonError(w, "Workflow not found", http.StatusNotFound)
		return wf, false
	case err != nil:
		h.JsonError(w, "Failed to get workflow: "+err.Error(), http.StatusInternalServerError)
		return wf, false
	}
	role, vhosts := h.GetRoleAndVHosts(r)
	if role != storage.RoleAdministrator && !h.HasVHostAccess(wf.VHost, vhosts) {
		h.JsonError(w, "Forbidden: you do not have access to this vhost", http.StatusForbidden)
		return wf, false
	}
	return wf, true
}

// List answers GET /api/workflows/{id}/proposals with {"data": [...]},
// newest first.
func (h *ProposalHandler) List(w http.ResponseWriter, r *http.Request) {
	wf, ok := h.workflow(w, r)
	if !ok {
		return
	}
	ps, err := h.svc.List(r.Context(), wf.ID)
	if err != nil {
		h.JsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"data": h.withEffect(r.Context(), ps)})
}

// proposalView is a proposal with the measured effect of the fix it applied.
type proposalView struct {
	selfheal.Proposal
	// Effect compares the flow before the fix with the flow since.
	Effect *advisor.Effect `json:"effect,omitempty"`
	// EffectPending says the effect will be measured once the flow has run
	// long enough since the fix.
	EffectPending bool `json:"effect_pending,omitempty"`
}

// withEffect measures the newest applied fix. Only the newest: readings
// since an older fix also hold every change made after it, so they would
// credit it with work it did not do.
func (h *ProposalHandler) withEffect(ctx context.Context, ps []selfheal.Proposal) []proposalView {
	out := make([]proposalView, len(ps))
	newest := -1
	for i, p := range ps {
		out[i] = proposalView{Proposal: p}
		if p.Status == selfheal.StatusApplied && p.DecidedAt != nil &&
			(newest < 0 || p.DecidedAt.After(*ps[newest].DecidedAt)) {
			newest = i
		}
	}
	adv := h.advisor()
	if newest < 0 || adv == nil || ps[newest].Baseline == nil {
		return out
	}
	p := ps[newest]
	after, ok := adv.WorkflowSummarySince(ctx, p.WorkflowID, *p.DecidedAt)
	if !ok {
		out[newest].EffectPending = true
		return out
	}
	effect := advisor.Compare(advisor.Summary(*p.Baseline), after)
	out[newest].Effect = &effect
	return out
}

// fixRequest names a recommendation to turn into a proposal. It names the
// finding only; what the fix changes is decided here, never by the caller.
type fixRequest struct {
	Rule   string `json:"rule"`
	NodeID string `json:"node_id"`
}

// ProposeFix answers POST /api/workflows/{id}/proposals: it stores the fix
// for one of the advisor's current findings as a pending proposal. Nothing
// changes until the proposal is approved.
func (h *ProposalHandler) ProposeFix(w http.ResponseWriter, r *http.Request) {
	wf, ok := h.workflow(w, r)
	if !ok {
		return
	}
	var req fixRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil || req.Rule == "" {
		h.JsonError(w, "The request must name a recommendation: {\"rule\": ..., \"node_id\": ...}", http.StatusBadRequest)
		return
	}
	adv := h.advisor()
	if adv == nil {
		h.JsonError(w, "The engine registry is not available", http.StatusServiceUnavailable)
		return
	}
	report := adv.WorkflowInsights(r.Context(), wf.ID)
	i := slices.IndexFunc(report.Findings, func(f advisor.Finding) bool {
		return f.Rule == req.Rule && f.NodeID == req.NodeID && f.Fix != ""
	})
	if i < 0 {
		h.JsonError(w, "This is not something Hermod currently recommends and can fix for this flow", http.StatusConflict)
		return
	}
	f := report.Findings[i]
	p, err := h.svc.ProposeFix(r.Context(), selfheal.Fix{WorkflowID: wf.ID, Kind: f.Fix, NodeID: f.NodeID, Reason: f.Detail})
	if err != nil {
		h.JsonError(w, err.Error(), statusOf(err))
		return
	}
	h.RecordAuditLog(r, "INFO", "Proposed a recommended fix for workflow "+wf.Name, "SELF_HEALING_PROPOSE", wf.ID, "", "", p)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(p)
}

// Approve applies a pending proposal.
func (h *ProposalHandler) Approve(w http.ResponseWriter, r *http.Request) {
	h.decide(w, r, h.approveWithBaseline, "Approved self-healing proposal", "SELF_HEALING_APPROVE")
}

// approveWithBaseline approves a proposal and keeps how the flow was running
// just before, so the fix's effect can be measured afterwards.
func (h *ProposalHandler) approveWithBaseline(ctx context.Context, workflowID, id, username string) (selfheal.Proposal, error) {
	var baseline *selfheal.Readings
	if adv := h.advisor(); adv != nil {
		if s := adv.WorkflowInsights(ctx, workflowID).Summary; s != nil {
			b := selfheal.Readings(*s)
			baseline = &b
		}
	}
	return h.svc.ApproveWithBaseline(ctx, workflowID, id, username, baseline)
}

// Reject closes a pending proposal without applying it.
func (h *ProposalHandler) Reject(w http.ResponseWriter, r *http.Request) {
	h.decide(w, r, h.svc.Reject, "Rejected self-healing proposal", "SELF_HEALING_REJECT")
}

// decision is Service.Approve or Service.Reject.
type decision func(ctx context.Context, workflowID, id, username string) (selfheal.Proposal, error)

func (h *ProposalHandler) decide(w http.ResponseWriter, r *http.Request, act decision, what, action string) {
	wf, ok := h.workflow(w, r)
	if !ok {
		return
	}
	username := "System"
	if user, _ := r.Context().Value(handlers.UserContextKey).(*storage.User); user != nil {
		username = user.Username
	}
	p, err := act(r.Context(), wf.ID, r.PathValue("pid"), username)
	if err != nil {
		h.JsonError(w, err.Error(), statusOf(err))
		return
	}
	h.RecordAuditLog(r, "INFO", what+" for workflow "+wf.Name, action, wf.ID, "", "", p)
	writeJSON(w, p)
}

func statusOf(err error) int {
	switch {
	case errors.Is(err, selfheal.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, selfheal.ErrNotPending), errors.Is(err, selfheal.ErrStale), errors.Is(err, selfheal.ErrNothingToFix):
		return http.StatusConflict
	case errors.Is(err, workflowhttp.ErrInvalidWorkflow):
		return http.StatusUnprocessableEntity
	default:
		return http.StatusInternalServerError
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
