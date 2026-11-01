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
