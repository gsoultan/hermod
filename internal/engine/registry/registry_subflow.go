// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package registry

import (
	"context"
	"errors"

	"github.com/gsoultan/hermod/internal/engine/registry/subflow"
	"github.com/gsoultan/hermod/internal/storage"
)

// errSubflowStarted refuses a sub-flow being turned on by itself.
var errSubflowStarted = errors.New("this is a sub-flow: it has no source of its own and runs inside the workflows that call it")

// workflowVersionReader is the part of storage that reads a saved version. It is
// not on RegistryStorage because only sub-flow calls need it.
type workflowVersionReader interface {
	GetWorkflowVersion(ctx context.Context, workflowID string, version int) (storage.WorkflowVersion, error)
}

// ExpandSubflows returns wf with every Run sub-flow step replaced by the steps
// of the sub-flow version it pins (see package subflow). A workflow that calls
// nothing is returned unchanged.
func (r *Registry) ExpandSubflows(ctx context.Context, wf storage.Workflow) (storage.Workflow, error) {
	if !subflow.HasCalls(wf) {
		return wf, nil
	}
	return subflow.Expand(ctx, wf, r.subflowLoader(wf.VHost))
}

// subflowLoader reads sub-flows for a caller in vhost. A sub-flow in another
// vhost reads as missing, the way the API answers for a workflow its caller
// cannot see.
func (r *Registry) subflowLoader(vhost string) subflow.Loader {
	return func(ctx context.Context, id string, version int) (storage.Workflow, error) {
		st := r.store()
		if st == nil {
			return storage.Workflow{}, errors.New("storage is not initialized")
		}
		current, err := st.GetWorkflow(ctx, id)
		if err != nil {
			return storage.Workflow{}, err
		}
		if current.VHost != vhost {
			return storage.Workflow{}, storage.ErrNotFound
		}
		if version == 0 {
			return current, nil
		}
		vr, ok := st.(workflowVersionReader)
		if !ok {
			return storage.Workflow{}, errors.New("this storage cannot read saved versions")
		}
		v, err := vr.GetWorkflowVersion(ctx, id, version)
		if err != nil {
			return storage.Workflow{}, err
		}
		pinned := current
		pinned.Nodes, pinned.Edges = v.Nodes, v.Edges
		return pinned, nil
	}
}

// SubflowCallers returns every Run sub-flow step in sub's vhost that runs sub.
func (r *Registry) SubflowCallers(ctx context.Context, sub storage.Workflow) ([]subflow.Caller, error) {
	st := r.store()
	if st == nil {
		return nil, errors.New("storage is not initialized")
	}
	all, _, err := st.ListWorkflows(ctx, storage.CommonFilter{VHost: sub.VHost})
	if err != nil {
		return nil, err
	}
	// An empty filter lists every vhost, so the vhost is checked here too.
	same := all[:0:0]
	for _, wf := range all {
		if wf.VHost == sub.VHost && wf.ID != sub.ID {
			same = append(same, wf)
		}
	}
	return subflow.Callers(same, sub.ID), nil
}

// BrokenCaller is a caller that would no longer start, and why.
type BrokenCaller struct {
	subflow.Caller
	Reason string `json:"reason"`
}

// BrokenCallers returns the callers that run sub's latest save and would no
// longer start if sub were saved as it is now: an input they do not map, a
// return they branch on that is gone. A caller that pins a version is never
// broken by a save, so it is not checked, and neither is one that cannot
// start already.
func (r *Registry) BrokenCallers(ctx context.Context, sub storage.Workflow) ([]BrokenCaller, error) {
	callers, err := r.SubflowCallers(ctx, sub)
	if err != nil {
		return nil, err
	}
	st := r.store()
	base := r.subflowLoader(sub.VHost)
	// The latest save of sub reads as the change about to be saved.
	load := func(ctx context.Context, id string, version int) (storage.Workflow, error) {
		if id == sub.ID && version == 0 {
			return sub, nil
		}
		return base(ctx, id, version)
	}
	var out []BrokenCaller
	checked := map[string]bool{}
	for _, c := range callers {
		if !c.Latest || checked[c.WorkflowID] {
			continue
		}
		checked[c.WorkflowID] = true
		wf, err := st.GetWorkflow(ctx, c.WorkflowID)
		if err != nil {
			return nil, err
		}
		// A caller that cannot start as things are was not broken by this save.
		if _, err := subflow.Expand(ctx, wf, base); err != nil {
			continue
		}
		if _, err := subflow.Expand(ctx, wf, load); err != nil {
			out = append(out, BrokenCaller{Caller: c, Reason: err.Error()})
		}
	}
	return out, nil
}
