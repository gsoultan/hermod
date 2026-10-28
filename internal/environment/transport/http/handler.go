// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

// Package http serves environments: the console's routes, behind a session,
// and the peer routes a linked instance calls with its token.
package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/environment"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/internal/version"
	workflowhttp "github.com/gsoultan/hermod/internal/workflow/transport/http"
	"github.com/gsoultan/hermod/pkg/security/secrets"
)

const (
	maxSmallBody     = 64 << 10
	maxCheckBody     = 1 << 20
	maxPromotionBody = 8 << 20
)

// Handler serves the environment routes.
type Handler struct {
	*handlers.Handler
	svc *environment.Service
}

// NewService builds the service the handler and the deploy watcher share.
// It reads h.Storage on every call, so a database chosen during first-run
// setup is the one it uses.
func NewService(h *handlers.Handler, remote environment.Remote) *environment.Service {
	return environment.NewService(liveStore{h}, workflowhttp.NewWorkflowHandler(h), remote, secretChecker{h}, version.Version)
}

// NewHandler serves svc's routes.
func NewHandler(h *handlers.Handler, svc *environment.Service) *Handler {
	return &Handler{Handler: h, svc: svc}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/environments/self", h.GetInstance)
	mux.Handle("PUT /api/environments/self", h.AdminOnly(h.PutInstance))
	mux.HandleFunc("GET /api/environments/rules", h.GetRules)
	mux.Handle("PUT /api/environments/rules", h.AdminOnly(h.PutRules))

	// Receiving side: who may send promotions here, and what they sent.
	mux.Handle("POST /api/environments/pairing-codes", h.AdminOnly(h.NewPairingCode))
	mux.Handle("GET /api/environments/peers", h.AdminOnly(h.ListPeers))
	mux.Handle("DELETE /api/environments/peers/{id}", h.AdminOnly(h.RevokePeer))
	mux.Handle("GET /api/environments/promotions", h.EditorOnly(h.ListPromotions))
	mux.Handle("POST /api/environments/promotions/{id}/approve", h.AdminOnly(h.Approve))
	mux.Handle("POST /api/environments/promotions/{id}/reject", h.AdminOnly(h.Reject))
	mux.Handle("POST /api/environments/promotions/{id}/rollback", h.AdminOnly(h.Rollback))

	// Sending side: where this instance promotes to.
	mux.Handle("GET /api/environments/links", h.EditorOnly(h.ListLinks))
	mux.Handle("POST /api/environments/links", h.AdminOnly(h.CreateLink))
	mux.Handle("DELETE /api/environments/links/{id}", h.AdminOnly(h.DeleteLink))
	mux.Handle("POST /api/environments/links/{id}/test", h.EditorOnly(h.TestLink))
	mux.Handle("GET /api/environments/links/{id}/matching", h.AdminOnly(h.Matching))
	mux.Handle("PUT /api/environments/links/{id}/mapping", h.AdminOnly(h.PutMapping))
	mux.Handle("POST /api/environments/links/{id}/preview", h.EditorOnly(h.Preview))
	mux.Handle("POST /api/environments/links/{id}/promotions", h.EditorOnly(h.Promote))
	mux.Handle("GET /api/environments/sent", h.EditorOnly(h.ListSent))

	h.registerPeerRoutes(mux)
}

// writeErr maps the service's errors to statuses.
func (h *Handler) writeErr(w http.ResponseWriter, err error) {
	var remote *environment.RemoteError
	switch {
	case errors.Is(err, environment.ErrNotFound), errors.Is(err, storage.ErrNotFound):
		h.JsonError(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, environment.ErrInvalid), errors.Is(err, environment.ErrBadPairingCode),
		errors.Is(err, workflowhttp.ErrInvalidWorkflow):
		h.JsonError(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, environment.ErrForbidden):
		h.JsonError(w, err.Error(), http.StatusForbidden)
	case errors.Is(err, environment.ErrConflict):
		h.JsonError(w, err.Error(), http.StatusConflict)
	case errors.Is(err, environment.ErrUnsupported):
		h.JsonError(w, err.Error(), http.StatusNotImplemented)
	case errors.As(err, &remote):
		// The linked instance refused; say so rather than look like our own
		// failure.
		h.JsonError(w, err.Error(), http.StatusBadGateway)
	default:
		h.JsonError(w, err.Error(), http.StatusBadGateway)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func currentUser(r *http.Request) *storage.User {
	u, _ := r.Context().Value(handlers.UserContextKey).(*storage.User)
	return u
}

func username(r *http.Request) string {
	if u := currentUser(r); u != nil {
		return u.Username
	}
	return "System"
}

func (h *Handler) GetInstance(w http.ResponseWriter, r *http.Request) {
	in, err := h.svc.Instance(r.Context())
	if err != nil {
		h.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, in)
}

func (h *Handler) PutInstance(w http.ResponseWriter, r *http.Request) {
	var in environment.Instance
	if !h.DecodeJSONBody(w, r, maxSmallBody, &in, "") {
		return
	}
	out, err := h.svc.SetInstance(r.Context(), in)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	h.RecordAuditLog(r, "INFO", "Described this instance as "+string(out.Role), "UPDATE", "", "", "", out)
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) GetRules(w http.ResponseWriter, r *http.Request) {
	rules, err := h.svc.Rules(r.Context())
	if err != nil {
		h.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rules)
}

func (h *Handler) PutRules(w http.ResponseWriter, r *http.Request) {
	var in environment.Rules
	if !h.DecodeJSONBody(w, r, maxSmallBody, &in, "") {
		return
	}
	out, err := h.svc.SetRules(r.Context(), in)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	h.RecordAuditLog(r, "INFO", "Changed the promotion rules", "UPDATE", "", "", "", out)
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) NewPairingCode(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Label string `json:"label"`
	}
	if r.ContentLength != 0 && !h.DecodeJSONBody(w, r, maxSmallBody, &in, "") {
		return
	}
	code, expires, err := h.svc.NewPairingCode(r.Context(), in.Label, username(r))
	if err != nil {
		h.writeErr(w, err)
		return
	}
	// The code is not in the audit entry: it is a credential until used.
	h.RecordAuditLog(r, "INFO", "Issued an environment pairing code", "CREATE", "", "", "", map[string]any{"label": in.Label})
	writeJSON(w, http.StatusCreated, map[string]any{"code": code, "expires_at": expires})
}

// peerView is a peer without its token hash.
type peerView struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	URL        string `json:"url"`
	CreatedBy  string `json:"created_by"`
	CreatedAt  any    `json:"created_at"`
	LastUsedAt any    `json:"last_used_at,omitempty"`
}

func (h *Handler) ListPeers(w http.ResponseWriter, r *http.Request) {
	peers, err := h.svc.Peers(r.Context())
	if err != nil {
		h.writeErr(w, err)
		return
	}
	out := make([]peerView, 0, len(peers))
	for _, p := range peers {
		v := peerView{ID: p.ID, Name: p.Name, URL: p.URL, CreatedBy: p.CreatedBy, CreatedAt: p.CreatedAt}
		if !p.LastUsedAt.IsZero() {
			v.LastUsedAt = p.LastUsedAt
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) RevokePeer(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.RevokePeer(r.Context(), r.PathValue("id")); err != nil {
		h.writeErr(w, err)
		return
	}
	h.RecordAuditLog(r, "INFO", "Revoked a linked environment", "DELETE", "", "", "", map[string]any{"peer": r.PathValue("id")})
	w.WriteHeader(http.StatusNoContent)
}

// visible keeps the promotions in vhosts the caller may see.
func (h *Handler) visible(r *http.Request, ps []environment.Promotion) []environment.Promotion {
	role, vhosts := h.GetRoleAndVHosts(r)
	if role == storage.RoleAdministrator {
		return ps
	}
	return slices.DeleteFunc(ps, func(p environment.Promotion) bool {
		vhost := p.VHost
		if vhost == "" {
			vhost = "default"
		}
		return !h.HasVHostAccess(vhost, vhosts)
	})
}

func (h *Handler) ListPromotions(w http.ResponseWriter, r *http.Request) {
	ps, err := h.svc.Promotions(r.Context())
	if err != nil {
		h.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, h.visible(r, ps))
}

func (h *Handler) Approve(w http.ResponseWriter, r *http.Request) {
	p, err := h.svc.Approve(r.Context(), r.PathValue("id"), username(r))
	if err != nil {
		if p.ID != "" {
			// Approved, but the deploy failed: the promotion says why.
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error(), "promotion": p})
			return
		}
		h.writeErr(w, err)
		return
	}
	h.RecordAuditLog(r, "INFO", "Approved and deployed a promotion from "+p.FromEnv, "PROMOTE", p.WorkflowID, "", "", p)
	writeJSON(w, http.StatusOK, p)
}

func (h *Handler) Reject(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Reason string `json:"reason"`
	}
	if r.ContentLength != 0 && !h.DecodeJSONBody(w, r, maxSmallBody, &in, "") {
		return
	}
	p, err := h.svc.Reject(r.Context(), r.PathValue("id"), username(r), in.Reason)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	h.RecordAuditLog(r, "INFO", "Rejected a promotion from "+p.FromEnv, "REJECT", p.WorkflowID, "", "", p)
	writeJSON(w, http.StatusOK, p)
}

func (h *Handler) Rollback(w http.ResponseWriter, r *http.Request) {
	p, err := h.svc.Rollback(r.Context(), r.PathValue("id"), username(r), "")
	if err != nil {
		h.writeErr(w, err)
		return
	}
	h.RecordAuditLog(r, "INFO", "Rolled back a promotion from "+p.FromEnv, "ROLLBACK", p.WorkflowID, "", "", p)
	writeJSON(w, http.StatusOK, p)
}

func (h *Handler) ListLinks(w http.ResponseWriter, r *http.Request) {
	links, err := h.svc.Links(r.Context())
	if err != nil {
		h.writeErr(w, err)
		return
	}
	if links == nil {
		links = []environment.Link{}
	}
	writeJSON(w, http.StatusOK, links)
}

func (h *Handler) CreateLink(w http.ResponseWriter, r *http.Request) {
	var in struct {
		URL     string `json:"url"`
		Code    string `json:"code"`
		SelfURL string `json:"self_url"`
	}
	if !h.DecodeJSONBody(w, r, maxSmallBody, &in, "") {
		return
	}
	l, err := h.svc.LinkWith(r.Context(), in.URL, in.Code, in.SelfURL, username(r))
	if err != nil {
		h.writeErr(w, err)
		return
	}
	h.RecordAuditLog(r, "INFO", "Linked environment "+l.Name+" at "+l.URL, "CREATE", "", "", "", l)
	writeJSON(w, http.StatusCreated, l)
}

func (h *Handler) DeleteLink(w http.ResponseWriter, r *http.Request) {
	note, err := h.svc.Unlink(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, err)
		return
	}
	h.RecordAuditLog(r, "INFO", "Unlinked an environment", "DELETE", "", "", "", map[string]any{"link": r.PathValue("id")})
	writeJSON(w, http.StatusOK, map[string]any{"note": note})
}

func (h *Handler) TestLink(w http.ResponseWriter, r *http.Request) {
	l, info, err := h.svc.TestLink(r.Context(), r.PathValue("id"))
	if err != nil && l.ID == "" {
		h.writeErr(w, err)
		return
	}
	out := map[string]any{"link": l, "info": info}
	if err != nil {
		out["error"] = err.Error()
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) Matching(w http.ResponseWriter, r *http.Request) {
	rows, err := h.svc.Matching(r.Context(), r.PathValue("id"), r.URL.Query().Get("vhost"))
	if err != nil {
		h.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (h *Handler) PutMapping(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Mapping map[string]string `json:"mapping"`
	}
	if !h.DecodeJSONBody(w, r, maxCheckBody, &in, "") {
		return
	}
	l, err := h.svc.SetMapping(r.Context(), r.PathValue("id"), in.Mapping)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, l)
}

type promoteRequest struct {
	WorkflowID string `json:"workflow_id"`
	environment.PromoteOptions
}

// mayPromote checks the caller may see the workflow's vhost.
func (h *Handler) mayPromote(w http.ResponseWriter, r *http.Request, workflowID string) bool {
	wf, err := h.Storage.GetWorkflow(r.Context(), workflowID)
	if err != nil {
		h.writeErr(w, err)
		return false
	}
	_, vhosts := h.GetRoleAndVHosts(r)
	vhost := wf.VHost
	if vhost == "" {
		vhost = "default"
	}
	if !h.HasVHostAccess(vhost, vhosts) {
		h.JsonError(w, "Forbidden: you do not have access to vhost "+vhost, http.StatusForbidden)
		return false
	}
	return true
}

func (h *Handler) Preview(w http.ResponseWriter, r *http.Request) {
	var in promoteRequest
	if !h.DecodeJSONBody(w, r, maxSmallBody, &in, "") {
		return
	}
	if !h.mayPromote(w, r, in.WorkflowID) {
		return
	}
	p, err := h.svc.Preview(r.Context(), r.PathValue("id"), in.WorkflowID, in.Version)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (h *Handler) Promote(w http.ResponseWriter, r *http.Request) {
	var in promoteRequest
	if !h.DecodeJSONBody(w, r, maxSmallBody, &in, "") {
		return
	}
	if !h.mayPromote(w, r, in.WorkflowID) {
		return
	}
	p, err := h.svc.Promote(r.Context(), r.PathValue("id"), in.WorkflowID, username(r), in.PromoteOptions)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	h.RecordAuditLog(r, "INFO", "Promoted version "+strconv.Itoa(p.FromVersion)+" to "+p.FromEnv, "PROMOTE", in.WorkflowID, "", "", p)
	writeJSON(w, http.StatusCreated, p)
}

func (h *Handler) ListSent(w http.ResponseWriter, r *http.Request) {
	ps, err := h.svc.Sent(r.Context())
	if err != nil {
		h.writeErr(w, err)
		return
	}
	if ps == nil {
		ps = []environment.Promotion{}
	}
	writeJSON(w, http.StatusOK, h.visible(r, ps))
}

// secretChecker answers from the vhost's own secrets and the HERMOD_SECRET_
// variables; a Vault or cloud secret manager is not asked, so a reference
// it answers can be reported missing, which is why that check only warns.
type secretChecker struct{ h *handlers.Handler }

func (c secretChecker) HasSecret(ctx context.Context, vhost, name string) bool {
	if vs, ok := c.h.Storage.(storage.VHostSecretStore); ok {
		if _, err := vs.GetVHostSecret(ctx, vhost, name); err == nil {
			return true
		}
	}
	_, ok := lookupEnv(secrets.DefaultEnvPrefix + name)
	return ok
}

func (c secretChecker) HasEnv(name string) bool {
	return secrets.LookupEnvVar(name) != ""
}
