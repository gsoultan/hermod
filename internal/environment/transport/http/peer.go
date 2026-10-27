// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package http

import (
	"context"
	"net/http"
	"os"
	"strings"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/environment"
	"github.com/gsoultan/hermod/internal/storage"
)

// pairAttemptsPerHour bounds pairing attempts per client address, on top of
// the service clearing every code after a run of wrong ones.
const pairAttemptsPerHour = 30

var lookupEnv = os.LookupEnv

// registerPeerRoutes serves what a linked instance calls. They are outside
// the session (AuthMiddleware lets /api/environments/pair and
// /api/environments/peer/ through): /pair is authorized by a one-time code,
// the rest by the peer's bearer token, checked here.
func (h *Handler) registerPeerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/environments/pair", h.Pair)
	mux.HandleFunc("GET /api/environments/peer/info", h.peer(h.PeerInfo))
	mux.HandleFunc("GET /api/environments/peer/connections", h.peer(h.PeerConnections))
	mux.HandleFunc("POST /api/environments/peer/check", h.peer(h.PeerCheck))
	mux.HandleFunc("POST /api/environments/peer/promotions", h.peer(h.PeerPromote))
	mux.HandleFunc("DELETE /api/environments/peer/self", h.peer(h.PeerUnpair))
}

type peerKey struct{}

// peer authenticates the bearer token and puts the peer in the context.
func (h *Handler) peer(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.Storage == nil {
			h.JsonError(w, "This instance is not set up yet", http.StatusServiceUnavailable)
			return
		}
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok {
			h.JsonError(w, "Unauthorized: a linked environment's token is required", http.StatusUnauthorized)
			return
		}
		p, err := h.svc.AuthenticatePeer(r.Context(), strings.TrimSpace(token))
		if err != nil {
			h.JsonError(w, "Unauthorized: this environment is not linked here, or its link was revoked", http.StatusUnauthorized)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), peerKey{}, p)))
	}
}

func peerFrom(r *http.Request) environment.Peer {
	p, _ := r.Context().Value(peerKey{}).(environment.Peer)
	return p
}

func (h *Handler) Pair(w http.ResponseWriter, r *http.Request) {
	if h.Storage == nil {
		h.JsonError(w, "This instance is not set up yet", http.StatusServiceUnavailable)
		return
	}
	if h.IsRateLimited(r, "environments-pair", pairAttemptsPerHour) {
		h.JsonError(w, "Too many pairing attempts; try again later", http.StatusTooManyRequests)
		return
	}
	var in environment.PairRequest
	if !h.DecodeJSONBody(w, r, maxSmallBody, &in, "") {
		return
	}
	res, err := h.svc.Pair(r.Context(), in)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	h.RecordAuditLog(r, "INFO", "Linked environment "+in.Name+" redeemed a pairing code", "CREATE", "", "", "",
		map[string]any{"peer": res.PeerID, "name": in.Name, "url": in.URL})
	writeJSON(w, http.StatusCreated, res)
}

func (h *Handler) PeerInfo(w http.ResponseWriter, r *http.Request) {
	info, err := h.svc.PeerInfo(r.Context())
	if err != nil {
		h.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (h *Handler) PeerConnections(w http.ResponseWriter, r *http.Request) {
	conns, err := h.svc.PeerConnections(r.Context())
	if err != nil {
		h.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, conns)
}

func (h *Handler) PeerCheck(w http.ResponseWriter, r *http.Request) {
	var in environment.CheckRequest
	if !h.DecodeJSONBody(w, r, maxCheckBody, &in, "") {
		return
	}
	res, err := h.svc.PeerCheck(r.Context(), in)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *Handler) PeerPromote(w http.ResponseWriter, r *http.Request) {
	var in environment.Submission
	if !h.DecodeJSONBody(w, r, maxPromotionBody, &in, "") {
		return
	}
	p := peerFrom(r)
	res, err := h.svc.Receive(r.Context(), p, in)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	h.RecordAuditLog(r, "INFO", "Received a promotion of "+res.WorkflowName+" from "+p.Name, "PROMOTE", res.WorkflowID, "", "",
		map[string]any{"promotion": res.ID, "sender": res.Sender, "status": res.Status})
	writeJSON(w, http.StatusCreated, res)
}

func (h *Handler) PeerUnpair(w http.ResponseWriter, r *http.Request) {
	p := peerFrom(r)
	if err := h.svc.RevokePeer(r.Context(), p.ID); err != nil {
		h.writeErr(w, err)
		return
	}
	h.RecordAuditLog(r, "INFO", "Linked environment "+p.Name+" unlinked itself", "DELETE", "", "", "", map[string]any{"peer": p.ID})
	w.WriteHeader(http.StatusNoContent)
}

// liveStore is environment.Store over whatever storage the handler holds
// now: first-run setup and a database switch replace it.
type liveStore struct{ h *handlers.Handler }

func (l liveStore) st() (storage.Storage, error) {
	if l.h.Storage == nil {
		return nil, environment.ErrUnsupported
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

func (l liveStore) GetWorkflowVersion(ctx context.Context, id string, v int) (storage.WorkflowVersion, error) {
	st, err := l.st()
	if err != nil {
		return storage.WorkflowVersion{}, err
	}
	return st.GetWorkflowVersion(ctx, id, v)
}

func (l liveStore) ListWorkflowVersions(ctx context.Context, id string) ([]storage.WorkflowVersion, error) {
	st, err := l.st()
	if err != nil {
		return nil, err
	}
	return st.ListWorkflowVersions(ctx, id)
}

func (l liveStore) ListSources(ctx context.Context, f storage.CommonFilter) ([]storage.Source, int, error) {
	st, err := l.st()
	if err != nil {
		return nil, 0, err
	}
	return st.ListSources(ctx, f)
}

func (l liveStore) ListSinks(ctx context.Context, f storage.CommonFilter) ([]storage.Sink, int, error) {
	st, err := l.st()
	if err != nil {
		return nil, 0, err
	}
	return st.ListSinks(ctx, f)
}

func (l liveStore) GetSource(ctx context.Context, id string) (storage.Source, error) {
	st, err := l.st()
	if err != nil {
		return storage.Source{}, err
	}
	return st.GetSource(ctx, id)
}

func (l liveStore) GetSink(ctx context.Context, id string) (storage.Sink, error) {
	st, err := l.st()
	if err != nil {
		return storage.Sink{}, err
	}
	return st.GetSink(ctx, id)
}
