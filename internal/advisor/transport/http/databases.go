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
	"time"

	"github.com/gsoultan/hermod/internal/advisor"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/infra/dbactivity"
)

// databasesView is the answer to the databases read.
type databasesView struct {
	Databases []advisor.DatabaseView `json:"databases"`
}

// Databases answers GET /api/workflows/{id}/insights/databases: each
// database the flow reaches, what Hermod did about it by itself, and what
// only a person can change. It reads what Hermod tracks in memory and
// connects to nothing, so access to the workflow's vhost is enough.
func (h *InsightsHandler) Databases(w http.ResponseWriter, r *http.Request) {
	wf, ok := h.workflow(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	names := h.workflowNames(ctx, wf)
	now := time.Now()
	out := databasesView{Databases: []advisor.DatabaseView{}}
	for _, id := range flowSourceIDs(wf) {
		src, err := h.Storage.GetSource(ctx, id)
		if err != nil {
			continue
		}
		snap, ok := h.snapshotOf(ctx, src)
		if !ok {
			continue
		}
		use := advisor.DatabaseUse{SourceID: src.ID, Source: src.Name, Snapshot: snap}
		out.Databases = append(out.Databases, advisor.ReadDatabase(use, wf.ID, names, now))
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (h *InsightsHandler) snapshotOf(ctx context.Context, src storage.Source) (dbactivity.Snapshot, bool) {
	if h.snapshot != nil {
		return h.snapshot(ctx, src)
	}
	return h.Registry.DatabaseSnapshot(ctx, src)
}

// workflowNames names flows by ID for the cards, reading each once. A flow
// that cannot be read is named by its ID.
func (h *InsightsHandler) workflowNames(ctx context.Context, self storage.Workflow) func(string) string {
	seen := map[string]string{self.ID: self.Name}
	return func(id string) string {
		if n, ok := seen[id]; ok {
			return n
		}
		n := id
		if wf, err := h.Storage.GetWorkflow(ctx, id); err == nil && wf.Name != "" {
			n = wf.Name
		}
		seen[id] = n
		return n
	}
}

// flowSourceIDs is every source the flow reads or queries, in the flow's
// order, each once: its source nodes, and the sources its DB Lookup and
// Execute SQL steps query.
func flowSourceIDs(wf storage.Workflow) []string {
	var out []string
	seen := map[string]bool{}
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, n := range wf.Nodes {
		if n.Type == "source" {
			add(n.RefID)
			continue
		}
		if id, _ := n.Config["sourceId"].(string); n.Type == "transformation" {
			add(id)
		}
	}
	return out
}
