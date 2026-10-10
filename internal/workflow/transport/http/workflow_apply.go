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
