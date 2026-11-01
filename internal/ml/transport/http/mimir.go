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
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gsoultan/hermod/internal/ml"
	"github.com/gsoultan/hermod/pkg/ml/inference"
	mimirsdk "github.com/gsoultan/mimir-server-sdk"
)

// mimirModelsRequest is what the Connect form sends to list a mimir server's
// models: the server's address and the name of the vhost secret holding the
// API key. The key itself is never sent by, or shown to, the browser.
type mimirModelsRequest struct {
	URL         string `json:"url"`
	TokenSecret string `json:"token_secret"`
}

// ListMimirModels answers the models a mimir server offers the key named by
// the request, for a person to pick one from. It needs the Editor role in
// the vhost, as saving the model does.
func (h *Handler) ListMimirModels(w http.ResponseWriter, r *http.Request) {
	vhost, _, ok := h.access(w, r, true)
	if !ok {
		return
	}
	var req mimirModelsRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxModelBody)).Decode(&req); err != nil {
		h.JsonError(w, `the body must be {"url": "http://mimir:8080", "token_secret": "MIMIR_API_KEY"}`, http.StatusBadRequest)
		return
	}
	models, err := h.service().ListMimirModels(r.Context(), vhost, req.URL, req.TokenSecret)
	if err != nil {
		h.failMimir(w, err)
		return
	}
	writeJSON(w, map[string]any{"models": models})
}

// failMimir reports why a mimir server could not be asked: a bad request in
// Hermod's words, the server's refusal in its own, and anything else as the
// server being unreachable.
func (h *Handler) failMimir(w http.ResponseWriter, err error) {
	var sdkErr *mimirsdk.Error
	switch {
	case errors.Is(err, ml.ErrBadMimirRequest):
		h.JsonError(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, mimirsdk.ErrUnauthenticated), errors.Is(err, mimirsdk.ErrPermissionDenied):
		h.JsonError(w, "the mimir server refused the API key: "+sdkMessage(err), http.StatusBadRequest)
	case errors.As(err, &sdkErr):
		h.JsonError(w, "the mimir server answered: "+sdkMessage(err), http.StatusBadGateway)
	default:
		h.JsonError(w, err.Error(), http.StatusBadGateway)
	}
}

// sdkMessage is the server's own explanation when the error carries one.
func sdkMessage(err error) string {
	var sdkErr *mimirsdk.Error
	if errors.As(err, &sdkErr) && sdkErr.Message != "" {
		return sdkErr.Message
	}
	return err.Error()
}

// explainRequest is the body of an explanation call: the row, and for a
// label model the label to explain, empty for the one the model gives.
type explainRequest struct {
	Instance map[string]any `json:"instance"`
	Label    string         `json:"label"`
}

// Explain answers why a model predicts what it does for one row: each
// column's effect on the answer against a typical row. Only a model on a
// mimir server explains itself; the Test modal's "Why?" asks here. Editor
// role, as a test prediction needs.
func (h *Handler) Explain(w http.ResponseWriter, r *http.Request) {
	vhost, _, ok := h.access(w, r, true)
	if !ok {
		return
	}
	var req explainRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxPredictBody)).Decode(&req); err != nil || req.Instance == nil {
		h.JsonError(w, `the body must be {"instance": {...}, "label": "optional"}`, http.StatusBadRequest)
		return
	}
	why, err := h.service().Explain(r.Context(), vhost, r.PathValue("name"), req.Instance, req.Label)
	if err != nil {
		h.failExplain(w, err)
		return
	}
	writeJSON(w, map[string]any{"model": r.PathValue("name"), "explanation": why})
}

// failExplain tells a missing model and a model that cannot explain apart
// from a server that would not answer.
func (h *Handler) failExplain(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ml.ErrModelNotFound):
		h.JsonError(w, "this vhost has no model by that name", http.StatusNotFound)
	case errors.Is(err, inference.ErrNotMimir):
		h.JsonError(w, err.Error(), http.StatusBadRequest)
	default:
		h.failMimir(w, err)
	}
}
