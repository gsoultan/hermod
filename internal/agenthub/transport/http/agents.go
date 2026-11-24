// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

// Package http is the REST surface of hermod-agent: adding an agent to a
// vhost and showing its token once, claiming its devices and saving how each
// is read, replacing or revoking the token, and what each agent last reported.
package http

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/agenthub"
	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
)

// maxAgentBody bounds a request: at most a few hundred devices' settings.
const maxAgentBody = 256 << 10

// Handler serves the agent routes.
type Handler struct {
	*handlers.Handler
	hub *agenthub.Hub
}

// NewHandler wraps the shared API handler. hub is the agent service of this
// process, told when an agent's settings or token change.
func NewHandler(h *handlers.Handler, hub *agenthub.Hub) *Handler {
	return &Handler{Handler: h, hub: hub}
}

// RegisterRoutes mounts the agent routes. Writes are behind EditorOnly, and
// each handler then checks the vhost, which a role wrapper cannot.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/vhosts/{vhost}/agents", h.ListAgents)
	mux.Handle("POST /api/vhosts/{vhost}/agents", h.EditorOnly(h.CreateAgent))
	mux.HandleFunc("GET /api/agents/{id}", h.GetAgent)
	mux.Handle("PUT /api/agents/{id}", h.EditorOnly(h.UpdateAgent))
	mux.Handle("POST /api/agents/{id}/token", h.EditorOnly(h.ReplaceToken))
	mux.Handle("DELETE /api/agents/{id}", h.EditorOnly(h.DeleteAgent))
	mux.Handle("POST /api/agents/{id}/devices/{device}/commands/{name}", h.EditorOnly(h.RunCommand))
}

// agentView is an agent as the API shows it: what is stored, and whether it
// is connected to this Hermod now. The token hash is never shown.
type agentView struct {
	Agent storage.Agent       `json:"agent"`
	Live  agenthub.LiveStatus `json:"live"`
}

// createResponse is the only answer that carries the token.
type createResponse struct {
	agentView
	Token string `json:"token"`
}

func (h *Handler) view(a storage.Agent) agentView {
	return agentView{Agent: a, Live: h.hub.Live(a.ID)}
}

func (h *Handler) store() (storage.AgentStore, bool) {
	h.StoreMu.RLock()
	defer h.StoreMu.RUnlock()
	as, ok := h.Storage.(storage.AgentStore)
	return as, ok
}

// user returns who is asking, refusing anyone who is not signed in or, for a
// write, is not an Editor.
func (h *Handler) user(w http.ResponseWriter, r *http.Request, write bool) (*storage.User, bool) {
	user, _ := r.Context().Value(handlers.UserContextKey).(*storage.User)
	if user == nil {
		h.JsonError(w, "Forbidden", http.StatusForbidden)
		return nil, false
	}
	if write && user.Role != storage.RoleAdministrator && user.Role != storage.RoleEditor {
		h.JsonError(w, "Forbidden: this needs the Editor role", http.StatusForbidden)
		return nil, false
	}
	return user, true
}

func (h *Handler) mayUse(user *storage.User, vhost string) bool {
	return user.Role == storage.RoleAdministrator || h.HasVHostAccess(vhost, user.VHosts)
}

// vhostAccess checks the vhost in the path.
func (h *Handler) vhostAccess(w http.ResponseWriter, r *http.Request, write bool) (string, *storage.User, storage.AgentStore, bool) {
	user, ok := h.user(w, r, write)
	if !ok {
		return "", nil, nil, false
	}
	vhost := r.PathValue("vhost")
	if vhost == "" || vhost == "all" {
		h.JsonError(w, "an agent belongs to one vhost: name it", http.StatusBadRequest)
		return "", nil, nil, false
	}
	if !h.mayUse(user, vhost) {
		h.JsonError(w, "Forbidden: you do not have access to this vhost", http.StatusForbidden)
		return "", nil, nil, false
	}
	as, ok := h.store()
	if !ok {
		h.JsonError(w, storage.ErrAgentsUnsupported.Error(), http.StatusNotImplemented)
		return "", nil, nil, false
	}
	return vhost, user, as, true
}

// agentAccess loads the agent in the path. An agent of a vhost the user may
// not use is answered as if it did not exist.
func (h *Handler) agentAccess(w http.ResponseWriter, r *http.Request, write bool) (storage.Agent, storage.AgentStore, bool) {
	user, ok := h.user(w, r, write)
	if !ok {
		return storage.Agent{}, nil, false
	}
	as, ok := h.store()
	if !ok {
		h.JsonError(w, storage.ErrAgentsUnsupported.Error(), http.StatusNotImplemented)
		return storage.Agent{}, nil, false
	}
	a, err := as.GetAgent(r.Context(), r.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) || (err == nil && !h.mayUse(user, a.VHost)) {
		h.JsonError(w, "agent not found", http.StatusNotFound)
		return storage.Agent{}, nil, false
	}
	if err != nil {
		h.JsonError(w, "Failed to read the agent", http.StatusInternalServerError)
		return storage.Agent{}, nil, false
	}
	return a, as, true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// ListAgents returns the vhost's agents.
func (h *Handler) ListAgents(w http.ResponseWriter, r *http.Request) {
	vhost, _, as, ok := h.vhostAccess(w, r, false)
	if !ok {
		return
	}
	agents, err := as.ListAgents(r.Context(), vhost)
	if err != nil {
		h.JsonError(w, "Failed to list the agents", http.StatusInternalServerError)
		return
	}
	out := make([]agentView, 0, len(agents))
	for _, a := range agents {
		out = append(out, h.view(a))
	}
	writeJSON(w, http.StatusOK, out)
}

// CreateAgent adds an agent to the vhost and returns it with its token, which
// is not shown again.
func (h *Handler) CreateAgent(w http.ResponseWriter, r *http.Request) {
	vhost, user, as, ok := h.vhostAccess(w, r, true)
	if !ok {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAgentBody)).Decode(&req); err != nil {
		h.JsonError(w, `the request body must be {"name": "..."}`, http.StatusBadRequest)
		return
	}
	a := storage.Agent{ID: uuid.NewString(), VHost: vhost, Name: strings.TrimSpace(req.Name), CreatedBy: user.Username}
	token, hash, err := storage.NewAgentToken(a.ID)
	if err != nil {
		h.JsonError(w, "Failed to issue a token", http.StatusInternalServerError)
		return
	}
	a.TokenHash = hash
	if err := storage.ValidateAgent(a); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := as.CreateAgent(r.Context(), a); err != nil {
		h.JsonError(w, "Failed to save the agent", http.StatusInternalServerError)
		return
	}
	saved, err := as.GetAgent(r.Context(), a.ID)
	if err != nil {
		saved = a
	}
	h.audit(r, "create", "Added agent "+a.Name, saved)
	writeJSON(w, http.StatusCreated, createResponse{agentView: h.view(saved), Token: token})
}

// GetAgent returns one agent.
func (h *Handler) GetAgent(w http.ResponseWriter, r *http.Request) {
	a, _, ok := h.agentAccess(w, r, false)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, h.view(a))
}

// UpdateAgent renames the agent and replaces its claimed devices' settings,
// and sends them to the agent if it is connected here.
func (h *Handler) UpdateAgent(w http.ResponseWriter, r *http.Request) {
	a, as, ok := h.agentAccess(w, r, true)
	if !ok {
		return
	}
	var req struct {
		Name    string                `json:"name"`
		Devices []storage.AgentDevice `json:"devices"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAgentBody)).Decode(&req); err != nil {
		h.JsonError(w, "the request body must be the agent's name and devices", http.StatusBadRequest)
		return
	}
	a.Name, a.Devices = strings.TrimSpace(req.Name), req.Devices
	if err := storage.ValidateAgent(a); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := as.UpdateAgentDevices(r.Context(), a.ID, a.Name, a.Devices); err != nil {
		h.JsonError(w, "Failed to save the agent", http.StatusInternalServerError)
		return
	}
	h.hub.SettingsChanged(r.Context(), a.ID)
	saved, err := as.GetAgent(r.Context(), a.ID)
	if err != nil {
		saved = a
	}
	h.audit(r, "update", "Saved the devices of agent "+a.Name, saved)
	writeJSON(w, http.StatusOK, h.view(saved))
}

// ReplaceToken issues the agent a new token, which revokes the old one and
// disconnects the agent until it is given the new one.
func (h *Handler) ReplaceToken(w http.ResponseWriter, r *http.Request) {
	a, as, ok := h.agentAccess(w, r, true)
	if !ok {
		return
	}
	token, hash, err := storage.NewAgentToken(a.ID)
	if err != nil {
		h.JsonError(w, "Failed to issue a token", http.StatusInternalServerError)
		return
	}
	if err := as.UpdateAgentToken(r.Context(), a.ID, hash); err != nil {
		h.JsonError(w, "Failed to save the token", http.StatusInternalServerError)
		return
	}
	h.hub.Disconnect(a.ID)
	h.audit(r, "update", "Replaced the token of agent "+a.Name, a)
	writeJSON(w, http.StatusOK, map[string]string{"token": token})
}

// DeleteAgent removes the agent and disconnects it.
func (h *Handler) DeleteAgent(w http.ResponseWriter, r *http.Request) {
	a, as, ok := h.agentAccess(w, r, true)
	if !ok {
		return
	}
	if err := as.DeleteAgent(r.Context(), a.ID); err != nil && !errors.Is(err, storage.ErrNotFound) {
		h.JsonError(w, "Failed to delete the agent", http.StatusInternalServerError)
		return
	}
	h.hub.Disconnect(a.ID)
	h.audit(r, "delete", "Deleted agent "+a.Name, a)
	w.WriteHeader(http.StatusNoContent)
}

// commandResponse is what a command run from the API did. The reply is
// given as text when it is valid UTF-8, and always as hex.
type commandResponse struct {
	Replied  bool   `json:"replied"`
	Reply    string `json:"reply,omitempty"`
	ReplyHex string `json:"reply_hex,omitempty"`
}

// RunCommand sends a device a command saved on it, to try it out. The body
// may give the fields its placeholders take: {"fields":{"target":12.5}}.
// Every attempt is audited, whatever its outcome.
func (h *Handler) RunCommand(w http.ResponseWriter, r *http.Request) {
	a, _, ok := h.agentAccess(w, r, true)
	if !ok {
		return
	}
	var req struct {
		Fields map[string]any `json:"fields"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAgentBody)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		h.JsonError(w, "the request body must be the command's fields", http.StatusBadRequest)
		return
	}
	device, name := r.PathValue("device"), r.PathValue("name")
	res, err := h.hub.RunCommand(r.Context(), a.VHost, a.ID, device, name, req.Fields)
	outcome := "ok"
	if err != nil {
		outcome = err.Error()
	}
	h.RecordAuditLog(r, "INFO", "Ran command "+name+" on device "+device+" of agent "+a.Name, "command", a.ID, "agent", "",
		map[string]any{"vhost": a.VHost, "name": a.Name, "device": device, "command": name, "outcome": outcome})
	var ce *agenthub.CommandError
	switch {
	case err == nil:
		out := commandResponse{Replied: res.Replied, ReplyHex: hex.EncodeToString(res.Reply)}
		if utf8.Valid(res.Reply) {
			out.Reply = string(res.Reply)
		}
		writeJSON(w, http.StatusOK, out)
	case errors.Is(err, agenthub.ErrAgentOffline) && !errors.As(err, &ce):
		h.JsonError(w, "the agent is not connected to this Hermod", http.StatusConflict)
	case errors.As(err, &ce):
		h.JsonError(w, ce.Error(), http.StatusBadGateway)
	case errors.Is(err, hermod.ErrPermanent):
		h.JsonError(w, err.Error(), http.StatusBadRequest)
	default:
		h.JsonError(w, "Failed to run the command", http.StatusInternalServerError)
	}
}

// audit records a change to an agent. The payload names the agent, its vhost
// and its devices; the token hash is not in it (it is not serialised).
func (h *Handler) audit(r *http.Request, action, msg string, a storage.Agent) {
	ids := make([]string, 0, len(a.Devices))
	for _, d := range a.Devices {
		ids = append(ids, d.DeviceID)
	}
	h.RecordAuditLog(r, "INFO", msg, action, a.ID, "agent", "", map[string]any{
		"vhost": a.VHost, "name": a.Name, "devices": ids,
	})
}
