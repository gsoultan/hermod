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
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/gsoultan/hermod/internal/storage"
)

// ErrInvalidWorkflow wraps the validator's verdict on a change it refused.
var ErrInvalidWorkflow = errors.New("invalid workflow")

// ErrVersionNotFound is returned when the version to roll back to cannot be
// read.
var ErrVersionNotFound = errors.New("cannot find the version to roll back to")

// ApplyWorkflowChange saves after in place of before through the same steps
// as an edit: validation, the stored row, and a new version. When the
// workflow has no history yet, before is recorded first, so the change can
// be rolled back. It returns the version holding the old state and the
// version holding the new one.
func (h *WorkflowHandler) ApplyWorkflowChange(ctx context.Context, before, after storage.Workflow, username, message string) (previous, applied int, err error) {
	if err := h.validateWorkflow(ctx, after); err != nil {
		return 0, 0, fmt.Errorf("%w: %w", ErrInvalidWorkflow, err)
	}
	previous = h.latestVersion(ctx, after.ID)
	if previous == 0 {
		previous = 1
		if err := h.recordVersion(ctx, before, previous, username, "Recorded before an automated change"); err != nil {
			return 0, 0, err
		}
	}
	if err := h.Storage.UpdateWorkflow(ctx, after); err != nil {
		return previous, 0, fmt.Errorf("updating workflow: %w", err)
	}
	applied = previous + 1
	if err := h.recordVersion(ctx, after, applied, username, message); err != nil {
		return previous, 0, err
	}
	return previous, applied, nil
}

// latestVersion is the workflow's newest version number, 0 for none.
func (h *WorkflowHandler) latestVersion(ctx context.Context, id string) int {
	versions, _ := h.Storage.ListWorkflowVersions(ctx, id)
	if len(versions) == 0 {
		return 0
	}
	return versions[0].Version
}

// recordVersion stores wf as version n of its history.
func (h *WorkflowHandler) recordVersion(ctx context.Context, wf storage.Workflow, n int, username, message string) error {
	// The config is the workflow without its graph, which is kept apart.
	cfg := wf
	cfg.Nodes = nil
	cfg.Edges = nil
	configJSON, _ := json.Marshal(cfg)

	if err := h.Storage.CreateWorkflowVersion(ctx, storage.WorkflowVersion{
		ID:             uuid.New().String(),
		WorkflowID:     wf.ID,
		Version:        n,
		Nodes:          wf.Nodes,
		Edges:          wf.Edges,
		TraceRetention: wf.TraceRetention,
		AuditRetention: wf.AuditRetention,
		Config:         string(configJSON),
		CreatedAt:      time.Now(),
		CreatedBy:      username,
		Message:        message,
	}); err != nil {
		return fmt.Errorf("recording version %d: %w", n, err)
	}
	return nil
}

// Validate returns the first error-level problem with wf, or nil.
func (h *WorkflowHandler) Validate(ctx context.Context, wf storage.Workflow) error {
	return h.validateWorkflow(ctx, wf)
}

// Portable returns wf as an export carries it: without this instance's
// runtime columns, captured samples or AI provider keys.
func (h *WorkflowHandler) Portable(wf storage.Workflow) storage.Workflow {
	return stripWorkflowRuntime(wf)
}

// CreateWorkflowVersioned validates and stores a new workflow and records it
// as version 1.
func (h *WorkflowHandler) CreateWorkflowVersioned(ctx context.Context, wf storage.Workflow, username, message string) (int, error) {
	if err := h.validateWorkflow(ctx, wf); err != nil {
		return 0, fmt.Errorf("%w: %w", ErrInvalidWorkflow, err)
	}
	if err := h.Storage.CreateWorkflow(ctx, wf); err != nil {
		return 0, fmt.Errorf("creating workflow: %w", err)
	}
	if err := h.recordVersion(ctx, wf, 1, username, message); err != nil {
		return 0, err
	}
	return 1, nil
}

// RollbackWorkflowTo restores the workflow as it was in version and records
// the restore as a new version, which it returns.
func (h *WorkflowHandler) RollbackWorkflowTo(ctx context.Context, id string, version int, username string) (int, error) {
	v, err := h.Storage.GetWorkflowVersion(ctx, id, version)
	if err != nil {
		return 0, fmt.Errorf("%w: version %d: %w", ErrVersionNotFound, version, err)
	}
	var wf storage.Workflow
	if err := json.Unmarshal([]byte(v.Config), &wf); err != nil {
		return 0, fmt.Errorf("parsing version %d: %w", version, err)
	}
	wf.ID = id
	wf.Nodes = v.Nodes
	wf.Edges = v.Edges
	wf.TraceRetention = v.TraceRetention
	wf.AuditRetention = v.AuditRetention
	if err := h.Storage.UpdateWorkflow(ctx, wf); err != nil {
		return 0, fmt.Errorf("restoring workflow: %w", err)
	}
	next := h.latestVersion(ctx, id) + 1
	if err := h.Storage.CreateWorkflowVersion(ctx, storage.WorkflowVersion{
		ID:             uuid.New().String(),
		WorkflowID:     id,
		Version:        next,
		Nodes:          wf.Nodes,
		Edges:          wf.Edges,
		TraceRetention: wf.TraceRetention,
		AuditRetention: wf.AuditRetention,
		Config:         v.Config,
		CreatedAt:      time.Now(),
		CreatedBy:      username,
		Message:        fmt.Sprintf("Rolled back to version %d", version),
	}); err != nil {
		return 0, fmt.Errorf("recording the rollback: %w", err)
	}
	return next, nil
}
