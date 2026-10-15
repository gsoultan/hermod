// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

// Package service is the lineage service: it loads one scope's stored records
// through a Loader, keeps only what the caller may see, and runs the lineage
// use cases on them.
package service

import (
	"context"
	"errors"

	"github.com/gsoultan/hermod/internal/lineage"
	"github.com/gsoultan/hermod/internal/storage"
)

// ErrForbidden is returned for a scope the caller may not read.
var ErrForbidden = errors.New("lineage: you do not have access to this vhost")

// Loader reads the stored records of one vhost, or of every vhost for "all".
type Loader interface {
	Load(ctx context.Context, vhost string) (lineage.Inputs, error)
}

// Scope is who asks about what.
type Scope struct {
	VHost  string
	Allows func(vhost string) bool
}

// Service answers lineage questions within a scope.
type Service struct {
	loader Loader
}

// New returns a Service over loader.
func New(loader Loader) *Service { return &Service{loader: loader} }

// all reports whether the scope is every vhost the caller may see, the way
// the workflow list reads an empty or "all" vhost.
func (sc Scope) all() bool { return sc.VHost == "" || sc.VHost == "all" }

// keep reports whether a record of vhost v belongs in the scope.
func (sc Scope) keep(v string) bool {
	if !sc.all() && v != sc.VHost {
		return false
	}
	return sc.Allows(v)
}

// inputs loads the scope's records and drops every one the caller may not
// see, so that a reference across vhosts resolves to nothing.
func (s *Service) inputs(ctx context.Context, scope Scope) (lineage.Inputs, error) {
	if scope.Allows == nil || (!scope.all() && !scope.Allows(scope.VHost)) {
		return lineage.Inputs{}, ErrForbidden
	}
	vhost := scope.VHost
	if scope.all() {
		vhost = "all"
	}
	in, err := s.loader.Load(ctx, vhost)
	if err != nil {
		return lineage.Inputs{}, err
	}
	in.Workflows = filter(in.Workflows, func(w storage.Workflow) bool { return scope.keep(w.VHost) })
	in.Sources = filter(in.Sources, func(r storage.Source) bool { return scope.keep(r.VHost) })
	in.Sinks = filter(in.Sinks, func(r storage.Sink) bool { return scope.keep(r.VHost) })
	in.Models = filter(in.Models, func(m storage.MLModel) bool { return scope.keep(m.VHost) })
	return in, nil
}

func filter[T any](items []T, keep func(T) bool) []T {
	out := items[:0:0]
	for _, it := range items {
		if keep(it) {
			out = append(out, it)
		}
	}
	return out
}

// Graph is the scope's lineage graph.
func (s *Service) Graph(ctx context.Context, scope Scope) (lineage.Graph, error) {
	in, err := s.inputs(ctx, scope)
	if err != nil {
		return lineage.Graph{}, err
	}
	return lineage.Derive(in), nil
}

// Impact is everything downstream of the node id within the scope. A node
// outside the scope is lineage.ErrUnknownNode, as one that does not exist.
func (s *Service) Impact(ctx context.Context, scope Scope, id string) (lineage.Impact, error) {
	g, err := s.Graph(ctx, scope)
	if err != nil {
		return lineage.Impact{}, err
	}
	return lineage.Downstream(g, id)
}

// WorkflowImpact is everything downstream of one workflow.
func (s *Service) WorkflowImpact(ctx context.Context, scope Scope, workflowID string) (lineage.Impact, error) {
	return s.Impact(ctx, scope, lineage.WorkflowNodeID(workflowID))
}
