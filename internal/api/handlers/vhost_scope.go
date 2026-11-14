// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package handlers

import (
	"context"
	"net/http"
	"sync"

	"github.com/gsoultan/hermod/internal/storage"
)

// VHostScope answers, for a caller who is not an Administrator, whether a
// workflow, source, sink or log entry is in one of the vhosts they were given.
//
// A vhost is the boundary between tenants, and most of what the API serves is
// keyed by a workflow's id rather than by a vhost: its logs, its live messages,
// its status. Those carry no vhost of their own, so it is looked up -- once per
// id for the life of the scope, which is one request or one stream.
//
// A nil scope allows everything. That is what ScopeOf returns for an
// Administrator and for a request the server made itself, so a caller never
// has to tell the two cases apart.
type VHostScope struct {
	h      *Handler
	ctx    context.Context
	vhosts []string

	mu   sync.Mutex
	seen map[string]bool
}

// ScopeOf returns the scope the request's caller is held to, or nil when the
// caller is not held to one.
func (h *Handler) ScopeOf(r *http.Request) *VHostScope {
	role, vhosts := h.GetRoleAndVHosts(r)
	if role == "" || role == storage.RoleAdministrator {
		return nil
	}
	return &VHostScope{h: h, ctx: r.Context(), vhosts: vhosts, seen: map[string]bool{}}
}

func (s *VHostScope) allowed(kind, id string) bool {
	if s == nil {
		return true
	}
	key := kind + "/" + id
	s.mu.Lock()
	ok, known := s.seen[key]
	s.mu.Unlock()
	if known {
		return ok
	}

	var (
		vhost string
		err   error
	)
	switch kind {
	case "workflow":
		var wf storage.Workflow
		wf, err = s.h.Storage.GetWorkflow(s.ctx, id)
		vhost = wf.VHost
	case "source":
		var src storage.Source
		src, err = s.h.Storage.GetSource(s.ctx, id)
		vhost = src.VHost
	default:
		var snk storage.Sink
		snk, err = s.h.Storage.GetSink(s.ctx, id)
		vhost = snk.VHost
	}
	// Something that cannot be read has no vhost to check, so it is an
	// Administrator's: a deleted workflow's entries, for one.
	ok = err == nil && s.h.HasVHostAccess(vhost, s.vhosts)

	s.mu.Lock()
	s.seen[key] = ok
	s.mu.Unlock()
	return ok
}

// Workflow reports whether the workflow is in the caller's vhosts.
func (s *VHostScope) Workflow(id string) bool { return s.allowed("workflow", id) }

// Source reports whether the source is in the caller's vhosts.
func (s *VHostScope) Source(id string) bool { return s.allowed("source", id) }

// Sink reports whether the sink is in the caller's vhosts.
func (s *VHostScope) Sink(id string) bool { return s.allowed("sink", id) }

// Log reports whether a log entry is about something in the caller's vhosts:
// everything it names must be, and it must name something. An entry that names
// nothing is about the platform.
func (s *VHostScope) Log(l storage.Log) bool {
	if s == nil {
		return true
	}
	if l.WorkflowID == "" && l.SourceID == "" && l.SinkID == "" {
		return false
	}
	return (l.WorkflowID == "" || s.Workflow(l.WorkflowID)) &&
		(l.SourceID == "" || s.Source(l.SourceID)) &&
		(l.SinkID == "" || s.Sink(l.SinkID))
}

// LogFilter reports whether the caller may ask for the logs a filter names. A
// request that names something outside their vhosts is refused, not answered
// with an empty list that says the id exists.
func (s *VHostScope) LogFilter(f storage.LogFilter) bool {
	return (f.WorkflowID == "" || s.Workflow(f.WorkflowID)) &&
		(f.SourceID == "" || s.Source(f.SourceID)) &&
		(f.SinkID == "" || s.Sink(f.SinkID))
}
