// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

// Package repository loads the stored records a lineage graph is derived
// from: a vhost's workflows, sources and sinks, page by page, and its ML
// models where the backend keeps them.
package repository

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/gsoultan/hermod/internal/lineage"
	"github.com/gsoultan/hermod/internal/storage"
)

// Store is the part of storage.Storage the repository reads.
type Store interface {
	ListWorkflows(ctx context.Context, filter storage.CommonFilter) ([]storage.Workflow, int, error)
	ListSources(ctx context.Context, filter storage.CommonFilter) ([]storage.Source, int, error)
	ListSinks(ctx context.Context, filter storage.CommonFilter) ([]storage.Sink, int, error)
}

// Repository reads lineage inputs from a Store.
type Repository struct {
	store      Store
	pageSize   int
	maxRecords int
}

// New returns a Repository over store.
func New(store Store) *Repository {
	return &Repository{store: store, pageSize: 500, maxRecords: 50000}
}

// Load reads the records of vhost, or of every vhost for "all". It reads
// every page: a graph built from the first page alone would show a source as
// having no consumers when they are on the second.
func (r *Repository) Load(ctx context.Context, vhost string) (lineage.Inputs, error) {
	var in lineage.Inputs
	var err error
	if in.Workflows, err = pages(ctx, r, vhost, "workflows", r.store.ListWorkflows); err != nil {
		return in, err
	}
	if in.Sources, err = pages(ctx, r, vhost, "sources", r.store.ListSources); err != nil {
		return in, err
	}
	if in.Sinks, err = pages(ctx, r, vhost, "sinks", r.store.ListSinks); err != nil {
		return in, err
	}
	in.Models, err = r.models(ctx, vhost, in)
	return in, err
}

// pages reads every page of one list.
func pages[T any](ctx context.Context, r *Repository, vhost, what string,
	list func(context.Context, storage.CommonFilter) ([]T, int, error)) ([]T, error) {
	var out []T
	for page := 1; ; page++ {
		items, total, err := list(ctx, storage.CommonFilter{VHost: vhost, Page: page, Limit: r.pageSize})
		if err != nil {
			return nil, fmt.Errorf("lineage: listing %s: %w", what, err)
		}
		out = append(out, items...)
		if len(out) > r.maxRecords {
			return nil, fmt.Errorf("lineage: more than %d %s in scope; narrow it to one vhost", r.maxRecords, what)
		}
		if len(items) < r.pageSize || len(out) >= total {
			return out, nil
		}
	}
}

// models reads the ML models of the vhost, or of every vhost a loaded record
// names, where the backend keeps models; a backend that does not has none.
func (r *Repository) models(ctx context.Context, vhost string, in lineage.Inputs) ([]storage.MLModel, error) {
	ms, ok := r.store.(interface {
		ListMLModels(ctx context.Context, vhost string) ([]storage.MLModel, error)
	})
	if !ok {
		return nil, nil
	}
	vhosts := []string{vhost}
	if vhost == "" || vhost == "all" {
		vhosts = []string{"default"}
		add := func(v string) {
			if v != "" && !slices.Contains(vhosts, v) {
				vhosts = append(vhosts, v)
			}
		}
		for _, w := range in.Workflows {
			add(w.VHost)
		}
		for _, s := range in.Sources {
			add(s.VHost)
		}
		for _, s := range in.Sinks {
			add(s.VHost)
		}
	}
	var out []storage.MLModel
	for _, v := range vhosts {
		models, err := ms.ListMLModels(ctx, v)
		if errors.Is(err, storage.ErrMLModelsUnsupported) {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("lineage: listing the ML models of %s: %w", v, err)
		}
		out = append(out, models...)
	}
	return out, nil
}
