package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/reply"
)

// Store is the part of storage the service reads and writes.
type Store interface {
	ListWorkflows(ctx context.Context, filter storage.CommonFilter) ([]storage.Workflow, int, error)
	GetWorkflow(ctx context.Context, id string) (storage.Workflow, error)
	GetSource(ctx context.Context, id string) (storage.Source, error)
	CreateWebhookRequest(ctx context.Context, req storage.WebhookRequest) error
}

// Caller is who is asking, as the transport authenticated them.
type Caller struct {
	// Name labels the caller in the run's request log.
	Name string
	// CanRun is whether the caller's role may start a workflow run (Editor or
	// Administrator). Reading is open to every role.
	CanRun bool
	// MayAccess reports whether the caller may see workflows in vhost.
	MayAccess func(vhost string) bool
}

var (
	// ErrNotFound is returned for a workflow that does not exist, is not
	// exposed, or is in a vhost the caller cannot see. The three are
	// deliberately indistinguishable, so the tool is not an oracle for which
	// workflow ids exist.
	ErrNotFound = errors.New("no MCP-exposed workflow with that id")
	// ErrNotAllowed is returned when the caller's role may not run workflows.
	ErrNotAllowed = errors.New("your role may read workflows but not run them; an Editor or Administrator can")
	// ErrNotRunnable is returned for an exposed workflow that has no webhook
	// source, which is the only entry point a run can be delivered through.
	ErrNotRunnable = errors.New("this workflow has no webhook source, so it cannot be run from MCP")
	// ErrNotListening is returned when the workflow's webhook source is not
	// running and could not be woken.
	ErrNotListening = errors.New("the workflow is not running, so nothing received the input; start it and try again")
)

// Service answers the MCP tools.
type Service struct {
	Store Store
	// Wake starts a parked workflow whose source has no listener, and reports
	// whether it started one. Optional.
	Wake func(ctx context.Context, resourceType, path string) bool
}

// WorkflowSummary is one entry of list_workflows.
type WorkflowSummary struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	VHost  string `json:"vhost,omitempty"`
	Active bool   `json:"active"`
	Status string `json:"status,omitempty"`
	// Runnable is whether run_workflow can deliver input to it.
	Runnable bool `json:"runnable"`
	// RepliesWithResult is whether run_workflow waits for and returns what
	// the workflow did, rather than only confirming the input was queued.
	RepliesWithResult bool `json:"replies_with_result"`
}

// WorkflowStatus is the answer to get_workflow_status.
type WorkflowStatus struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	VHost     string `json:"vhost,omitempty"`
	Active    bool   `json:"active"`
	Status    string `json:"status,omitempty"`
	Processed uint64 `json:"processed"`
	Errors    uint64 `json:"errors"`
	Lag       uint64 `json:"lag"`
}

// RunResult is the answer to run_workflow.
type RunResult struct {
	// ID is the id of the message the input became.
	ID string `json:"id"`
	// Status is "dispatched" for a workflow that does not reply, "pending"
	// when the wait ran out before the workflow finished, and otherwise what
	// the workflow did: delivered, completed, filtered, dead_lettered or
	// failed.
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
	// Record is the message as the workflow left it, when it replied.
	Record any `json:"record,omitempty"`
}

// listPageSize is how many workflows List reads per storage call. Storage has
// no tag filter, so the exposed ones are picked out here.
const listPageSize = 200

// List returns the exposed workflows the caller can see.
func (s *Service) List(ctx context.Context, c Caller) ([]WorkflowSummary, error) {
	out := []WorkflowSummary{}
	for page := 1; ; page++ {
		wfs, total, err := s.Store.ListWorkflows(ctx, storage.CommonFilter{Page: page, Limit: listPageSize})
		if err != nil {
			return nil, fmt.Errorf("listing workflows: %w", err)
		}
		for _, wf := range wfs {
			if !visible(c, wf) {
				continue
			}
			sum := WorkflowSummary{ID: wf.ID, Name: wf.Name, VHost: wf.VHost, Active: wf.Active, Status: wf.Status}
			src, ok, err := s.webhookSource(ctx, wf)
			if err != nil {
				return nil, err
			}
			if ok {
				sum.Runnable = true
				sum.RepliesWithResult, _ = reply.ModeOf(src.Config)
			}
			out = append(out, sum)
		}
		if len(wfs) < listPageSize || page*listPageSize >= total {
			return out, nil
		}
	}
}

// Status returns one exposed workflow's status.
func (s *Service) Status(ctx context.Context, c Caller, id string) (WorkflowStatus, error) {
	wf, err := s.lookup(ctx, c, id)
	if err != nil {
		return WorkflowStatus{}, err
	}
	return WorkflowStatus{
		ID: wf.ID, Name: wf.Name, VHost: wf.VHost, Active: wf.Active, Status: wf.Status,
		Processed: wf.TotalProcessed, Errors: wf.TotalErrors, Lag: wf.TotalLag,
	}, nil
}

// lookup returns the workflow if the caller may use it over MCP.
func (s *Service) lookup(ctx context.Context, c Caller, id string) (storage.Workflow, error) {
	if strings.TrimSpace(id) == "" {
		return storage.Workflow{}, ErrNotFound
	}
	wf, err := s.Store.GetWorkflow(ctx, id)
	if errors.Is(err, storage.ErrNotFound) {
		return storage.Workflow{}, ErrNotFound
	}
	if err != nil {
		return storage.Workflow{}, fmt.Errorf("reading workflow: %w", err)
	}
	if !visible(c, wf) {
		return storage.Workflow{}, ErrNotFound
	}
	return wf, nil
}

func visible(c Caller, wf storage.Workflow) bool {
	return IsExposed(wf) && c.MayAccess != nil && c.MayAccess(wf.VHost)
}

// webhookSource returns the source a run is delivered to: the first source
// node whose source is a webhook with a path.
func (s *Service) webhookSource(ctx context.Context, wf storage.Workflow) (storage.Source, bool, error) {
	for _, n := range wf.Nodes {
		if n.Type != "source" || n.RefID == "" {
			continue
		}
		src, err := s.Store.GetSource(ctx, n.RefID)
		if errors.Is(err, storage.ErrNotFound) {
			continue
		}
		if err != nil {
			return storage.Source{}, false, fmt.Errorf("reading source %s: %w", n.RefID, err)
		}
		if src.Type == "webhook" && src.Config["path"] != "" {
			return src, true, nil
		}
	}
	return storage.Source{}, false, nil
}
