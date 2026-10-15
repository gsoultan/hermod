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
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// discoveryTimeout bounds listing a source's tables or columns, as the
// connector discovery routes do; previewTimeout bounds a preview query.
const (
	discoveryTimeout = 15 * time.Second
	previewTimeout   = 30 * time.Second
)

// engineUp refuses a query builder call while the engine, which opens the
// source, is not running.
func (h *Handler) engineUp(w http.ResponseWriter) bool {
	if h.Registry == nil {
		h.JsonError(w, "reading from a database needs the engine, which is not running yet", http.StatusServiceUnavailable)
		return false
	}
	return true
}

// SourceTables lists the tables of one of the vhost's database sources, for
// the dataset query builder. The source is named by id: its credentials
// never reach the browser.
func (h *Handler) SourceTables(w http.ResponseWriter, r *http.Request) {
	vhost, _, ok := h.access(w, r, true)
	if !ok || !h.engineUp(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), discoveryTimeout)
	defer cancel()
	tables, err := h.Registry.MLSourceTables(ctx, vhost, r.PathValue("id"))
	if err != nil {
		h.fail(w, err)
		return
	}
	if tables == nil {
		tables = []string{}
	}
	writeJSON(w, tables)
}

// SourceColumns lists the columns of one table of the source.
func (h *Handler) SourceColumns(w http.ResponseWriter, r *http.Request) {
	vhost, _, ok := h.access(w, r, true)
	if !ok {
		return
	}
	table := r.URL.Query().Get("table")
	if table == "" {
		h.JsonError(w, "name the table: ?table=...", http.StatusBadRequest)
		return
	}
	if !h.engineUp(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), discoveryTimeout)
	defer cancel()
	cols, err := h.Registry.MLSourceColumns(ctx, vhost, r.PathValue("id"), table)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, cols)
}

// previewRequest is a dataset query to try before filling a dataset.
type previewRequest struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

// PreviewQuery runs a dataset query on the source and returns its first
// rows, so the query can be checked before a dataset is filled from it.
func (h *Handler) PreviewQuery(w http.ResponseWriter, r *http.Request) {
	vhost, _, ok := h.access(w, r, true)
	if !ok {
		return
	}
	var req previewRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxModelBody)).Decode(&req); err != nil ||
		strings.TrimSpace(req.Query) == "" {
		h.JsonError(w, `the body must hold a query: {"query": "SELECT ..."}`, http.StatusBadRequest)
		return
	}
	if !h.engineUp(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), previewTimeout)
	defer cancel()
	p, err := h.Registry.MLPreviewQuery(ctx, vhost, r.PathValue("id"), req.Query, req.Limit)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, p)
}
