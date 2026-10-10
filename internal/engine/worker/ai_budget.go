package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/gsoultan/hermod/pkg/llm"
)

// A worker reaches the AI budgets through the control plane: it has no
// database to read a budget from or count usage in, and counting on the
// control plane keeps one total however many workers share a vhost. These make
// the worker's storage an aibudget.Remote. See internal/aibudget/transport/http.

// CheckAIBudget asks whether a model call may go ahead. Anything but a clear
// yes refuses the call: an unreachable control plane cannot vouch for the
// budget, and budgets fail closed.
func (c *WorkerAPIClient) CheckAIBudget(ctx context.Context, vhost, workflowID string) error {
	unavailable := func(err error) error {
		return &llm.BudgetError{Limit: llm.LimitUnavailable, VHost: vhost, WorkflowID: workflowID, Cause: err}
	}
	resp, err := c.doRequest(ctx, http.MethodPost, "/api/worker/ai/check",
		map[string]string{"vhost": vhost, "workflow_id": workflowID})
	if err != nil {
		return unavailable(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return unavailable(fmt.Errorf("control plane answered %s", resp.Status))
	}
	var body struct {
		Allowed    bool   `json:"allowed"`
		Limit      string `json:"limit"`
		VHost      string `json:"vhost"`
		WorkflowID string `json:"workflow_id"`
		Used       int64  `json:"used"`
		Max        int64  `json:"max"`
		Message    string `json:"message"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&body); err != nil {
		return unavailable(fmt.Errorf("unreadable answer: %w", err))
	}
	if body.Allowed {
		return nil
	}
	be := &llm.BudgetError{Limit: llm.Limit(body.Limit), VHost: body.VHost, WorkflowID: body.WorkflowID, Used: body.Used, Max: body.Max}
	if be.Limit == llm.LimitUnavailable {
		be.Cause = fmt.Errorf("%s", body.Message)
	}
	return be
}

// RecordAIUsage reports a finished call's tokens; the control plane prices
// and counts them.
func (c *WorkerAPIClient) RecordAIUsage(ctx context.Context, vhost, workflowID string, rec llm.CallRecord) error {
	resp, err := c.doRequest(ctx, http.MethodPost, "/api/worker/ai/usage", map[string]any{
		"vhost": vhost, "workflow_id": workflowID, "provider": rec.Provider, "model": rec.Model,
		"input_tokens": rec.Usage.InputTokens, "output_tokens": rec.Usage.OutputTokens,
	})
	if err != nil {
		return fmt.Errorf("reporting AI usage of vhost %q: %w", vhost, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("reporting AI usage of vhost %q: API error: %s", vhost, resp.Status)
	}
	return nil
}
