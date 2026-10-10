package selfheal

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gsoultan/hermod/internal/optimizer"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/engine/config"
)

// Retry caps a proposal never goes past.
const (
	maxRetriesCap    = 10
	retryIntervalCap = 10 * time.Second
)

// Store is what the service reads and writes.
type Store interface {
	Settings
	GetWorkflow(ctx context.Context, id string) (storage.Workflow, error)
}

// Applier saves a changed workflow through the workflow update path and
// returns the version that held the workflow before and the version the
// change was recorded as.
type Applier interface {
	ApplyWorkflowChange(ctx context.Context, before, after storage.Workflow, username, message string) (previous, applied int, err error)
}

// Service stores, lists and decides proposals.
type Service struct {
	store   Store
	applier Applier
	// mu serialises read-modify-write of a workflow's proposal list within
	// this process.
	mu  sync.Mutex
	now func() time.Time
}

// NewService builds the service. A nil applier gives a service that can
// propose and list but not approve, which is what the engine side needs.
func NewService(store Store, applier Applier) *Service {
	return &Service{store: store, applier: applier, now: time.Now}
}

// Propose records a suggestion from the self-correction gate as a pending
// proposal. A suggestion that would change nothing is dropped, and one that
// repeats a pending proposal of the same kind only counts again.
func (s *Service) Propose(ctx context.Context, sug optimizer.Suggestion) error {
	if sug.Action != optimizer.ActionIncreaseRetry {
		return fmt.Errorf("no proposal is made for %q", sug.Action)
	}
	wf, err := s.store.GetWorkflow(ctx, sug.WorkflowID)
	if err != nil {
		return err
	}
	patch := retryPatch(wf)
	if len(patch) == 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	ps, err := load(ctx, s.store, wf.ID)
	if err != nil {
		return err
	}
	now := s.now().UTC()
	if i := slices.IndexFunc(ps, func(p Proposal) bool {
		return p.Status == StatusPending && p.Kind == string(sug.Action)
	}); i >= 0 {
		ps[i].Occurrences++
		ps[i].LastSeenAt = now
		return save(ctx, s.store, wf.ID, ps)
	}
	ps = append(ps, Proposal{
		ID: uuid.NewString(), WorkflowID: wf.ID, Kind: string(sug.Action), NodeID: sug.NodeID,
		Title: "Retry failed deliveries more patiently", Reason: sug.Reason, Patch: patch,
		Status: StatusPending, Occurrences: 1, CreatedAt: now, LastSeenAt: now,
	})
	return save(ctx, s.store, wf.ID, ps)
}

// retryPatch raises the workflow's retry count by one and doubles its retry
// interval, each from the value the engine actually uses, within the caps.
func retryPatch(wf storage.Workflow) []Change {
	defaults := config.DefaultConfig()
	var patch []Change

	retries := wf.MaxRetries
	if retries <= 0 {
		retries = defaults.MaxRetries
	}
	if next := min(retries+1, maxRetriesCap); next != wf.MaxRetries && retries < maxRetriesCap {
		patch = append(patch, replace("/max_retries", wf.MaxRetries, next))
	}

	interval := defaults.RetryInterval
	if wf.RetryInterval != "" {
		d, err := time.ParseDuration(wf.RetryInterval)
		if err != nil {
			return patch // a format this does not read is left alone
		}
		interval = d
	}
	if interval > 0 && interval < retryIntervalCap {
		patch = append(patch, replace("/retry_interval", wf.RetryInterval, min(interval*2, retryIntervalCap).String()))
	}
	return patch
}

// List returns a workflow's proposals, newest first.
func (s *Service) List(ctx context.Context, workflowID string) ([]Proposal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ps, err := load(ctx, s.store, workflowID)
	if err != nil {
		return nil, err
	}
	slices.Reverse(ps)
	if ps == nil {
		ps = []Proposal{}
	}
	return ps, nil
}

// Approve applies a pending proposal through the workflow update path.
func (s *Service) Approve(ctx context.Context, workflowID, id, username string) (Proposal, error) {
	if s.applier == nil {
		return Proposal{}, ErrNoApplier
	}
	return s.decide(ctx, workflowID, id, func(p *Proposal) error {
		before, err := s.store.GetWorkflow(ctx, workflowID)
		if err != nil {
			return err
		}
		after, err := patched(before, p.Patch)
		if errors.Is(err, ErrStale) {
			p.Status = StatusStale
			return err
		}
		if err != nil {
			return err
		}
		prev, applied, err := s.applier.ApplyWorkflowChange(ctx, before, after, username,
			fmt.Sprintf("Self-healing: %s (proposal %s)", p.Title, p.ID))
		if err != nil {
			return err
		}
		p.Status, p.PreviousVersion, p.AppliedVersion = StatusApplied, prev, applied
		return nil
	}, username)
}

// Reject closes a pending proposal without changing the workflow.
func (s *Service) Reject(ctx context.Context, workflowID, id, username string) (Proposal, error) {
	return s.decide(ctx, workflowID, id, func(p *Proposal) error {
		p.Status = StatusRejected
		return nil
	}, username)
}

// decide runs act on a pending proposal and stores the outcome. A proposal
// act leaves pending is stored unchanged; one it moves on (applied, rejected
// or stale) is stamped with who decided and when.
func (s *Service) decide(ctx context.Context, workflowID, id string, act func(*Proposal) error, username string) (Proposal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ps, err := load(ctx, s.store, workflowID)
	if err != nil {
		return Proposal{}, err
	}
	i := slices.IndexFunc(ps, func(p Proposal) bool { return p.ID == id })
	if i < 0 {
		return Proposal{}, ErrNotFound
	}
	if ps[i].Status != StatusPending {
		return ps[i], ErrNotPending
	}
	p := ps[i]
	actErr := act(&p)
	if p.Status == StatusPending {
		return p, actErr
	}
	now := s.now().UTC()
	p.DecidedAt, p.DecidedBy = &now, username
	ps[i] = p
	if err := save(ctx, s.store, workflowID, ps); err != nil {
		return p, errors.Join(actErr, err)
	}
	return p, actErr
}
