// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package environment

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gsoultan/hermod/internal/storage"
)

// maxCheckItems bounds each list in a CheckRequest.
const maxCheckItems = 500

// PeerCheck answers a linked instance's CheckRequest.
func (s *Service) PeerCheck(ctx context.Context, req CheckRequest) (CheckResult, error) {
	if len(req.Sources) > maxCheckItems || len(req.Sinks) > maxCheckItems ||
		len(req.Secrets) > maxCheckItems || len(req.Envs) > maxCheckItems {
		return CheckResult{}, fmt.Errorf("%w: too many items to check", ErrInvalid)
	}
	res := CheckResult{
		MissingSources: missing(req.Sources, func(id string) bool { _, err := s.store.GetSource(ctx, id); return err == nil }),
		MissingSinks:   missing(req.Sinks, func(id string) bool { _, err := s.store.GetSink(ctx, id); return err == nil }),
	}
	vhost := req.VHost
	if req.WorkflowID != "" {
		if wf, err := s.store.GetWorkflow(ctx, req.WorkflowID); err == nil {
			vhost = wf.VHost
			res.Current = s.current(ctx, wf)
		}
	}
	res.MissingSecrets = missing(req.Secrets, func(name string) bool { return s.secrets != nil && s.secrets.HasSecret(ctx, vhost, name) })
	res.MissingEnvs = missing(req.Envs, func(name string) bool { return s.secrets != nil && s.secrets.HasEnv(name) })
	return res, nil
}

// missing lists the items has says are absent, never nil.
func missing(items []string, has func(string) bool) []string {
	out := []string{}
	for _, it := range items {
		if !has(it) {
			out = append(out, it)
		}
	}
	return out
}

// current is wf as a sender diffs against, with its latest version number.
func (s *Service) current(ctx context.Context, wf storage.Workflow) *CurrentWorkflow {
	cur := &CurrentWorkflow{Workflow: s.applier.Portable(wf)}
	if versions, err := s.store.ListWorkflowVersions(ctx, wf.ID); err == nil && len(versions) > 0 {
		cur.Version = versions[0].Version
	}
	return cur
}

// Receive records a promotion a peer sent. It is checked here as well as on
// the sender: every connection it names must exist on this instance, and the
// version must have soaked as long as the rules say. Under the "none"
// approval rule it is deployed at once.
func (s *Service) Receive(ctx context.Context, peer Peer, sub Submission) (Promotion, error) {
	wf := sub.Workflow
	if err := validSubmission(sub); err != nil {
		return Promotion{}, err
	}
	rules, err := loadRules(ctx, s.store)
	if err != nil {
		return Promotion{}, err
	}
	if why := soakProblem(rules, sub.FromVersionAt, s.now()); why != "" {
		return Promotion{}, fmt.Errorf("%w: %s", ErrInvalid, why)
	}
	if err := s.hasConnections(ctx, wf); err != nil {
		return Promotion{}, err
	}

	// What it changes is worked out here, against what this instance runs,
	// never taken from the sender.
	var current *storage.Workflow
	if existing, err := s.store.GetWorkflow(ctx, wf.ID); err == nil {
		portable := s.applier.Portable(existing)
		current = &portable
		wf.VHost = existing.VHost
	} else if !errors.Is(err, storage.ErrNotFound) {
		return Promotion{}, fmt.Errorf("reading workflow %s: %w", wf.ID, err)
	}
	wf = s.applier.Portable(wf)

	p := Promotion{
		ID: uuid.NewString(), PeerID: peer.ID, FromEnv: peer.Name, WorkflowID: wf.ID, WorkflowName: wf.Name,
		VHost: wf.VHost, FromVersion: sub.FromVersion, Sender: strings.TrimSpace(sub.Sender),
		Note: strings.TrimSpace(sub.Note), Start: sub.Start, AutoRollback: sub.AutoRollback,
		Changes: Diff(current, wf), Status: StatusPending, CreatedAt: s.now().UTC(),
	}

	s.mu.Lock()
	ps, err := loadPromotions(ctx, s.store)
	if err == nil {
		err = putJSON(ctx, s.store, keyPromotionBody+p.ID, wf)
	}
	if err == nil {
		err = savePromotions(ctx, s.store, append(ps, p))
	}
	s.mu.Unlock()
	if err != nil {
		return Promotion{}, err
	}

	if rules.Approval == ApproveNone {
		return s.deploy(ctx, p.ID, "automatic")
	}
	return p, nil
}

func validSubmission(sub Submission) error {
	switch {
	case sub.Workflow.ID == "" || len(sub.Workflow.ID) > 128:
		return fmt.Errorf("%w: the workflow needs an ID of at most 128 characters", ErrInvalid)
	case strings.TrimSpace(sub.Workflow.Name) == "":
		return fmt.Errorf("%w: the workflow needs a name", ErrInvalid)
	case len(sub.Sender) > 128 || len(sub.Note) > 1000:
		return fmt.Errorf("%w: the sender or note is too long", ErrInvalid)
	}
	return nil
}

// hasConnections checks every connection wf names exists here.
func (s *Service) hasConnections(ctx context.Context, wf storage.Workflow) error {
	srcIDs, sinkIDs := workflowRefs(wf)
	for _, id := range srcIDs {
		if _, err := s.store.GetSource(ctx, id); err != nil {
			return fmt.Errorf("%w: the workflow uses source %s, which this instance does not have", ErrInvalid, id)
		}
	}
	for _, id := range sinkIDs {
		if _, err := s.store.GetSink(ctx, id); err != nil {
			return fmt.Errorf("%w: the workflow uses sink %s, which this instance does not have", ErrInvalid, id)
		}
	}
	return nil
}

// Promotions lists the promotions sent here, newest first.
func (s *Service) Promotions(ctx context.Context) ([]Promotion, error) {
	ps, err := loadPromotions(ctx, s.store)
	if err != nil {
		return nil, err
	}
	slices.Reverse(ps)
	return ps, nil
}

// update applies fn to one promotion under the lock and saves it.
func (s *Service) update(ctx context.Context, id string, fn func(*Promotion) error) (Promotion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ps, err := loadPromotions(ctx, s.store)
	if err != nil {
		return Promotion{}, err
	}
	i := slices.IndexFunc(ps, func(p Promotion) bool { return p.ID == id })
	if i < 0 {
		return Promotion{}, ErrNotFound
	}
	if err := fn(&ps[i]); err != nil {
		return ps[i], err
	}
	return ps[i], savePromotions(ctx, s.store, ps)
}

// Approve deploys a pending promotion. Under the admin_except_sender rule
// the person who sent it cannot approve it.
func (s *Service) Approve(ctx context.Context, id, username string) (Promotion, error) {
	rules, err := loadRules(ctx, s.store)
	if err != nil {
		return Promotion{}, err
	}
	// Claim it first, so two admins approving at once deploy it once.
	_, err = s.update(ctx, id, func(p *Promotion) error {
		if p.Status != StatusPending {
			return fmt.Errorf("%w: this promotion is %s, not pending", ErrConflict, p.Status)
		}
		if rules.Approval == ApproveAnyAdminExceptSender && strings.EqualFold(p.Sender, username) {
			return fmt.Errorf("%w: you sent this promotion, so another admin has to approve it", ErrForbidden)
		}
		p.Status = StatusDeploying
		p.DecidedBy, p.DecidedAt = username, s.now().UTC()
		return nil
	})
	if err != nil {
		return Promotion{}, err
	}
	return s.deploy(ctx, id, username)
}

// deploy stores the promoted workflow through the workflow update path and
// records the outcome on the promotion.
func (s *Service) deploy(ctx context.Context, id, by string) (Promotion, error) {
	wf, err := loadPromotionWorkflow(ctx, s.store, id)
	if err != nil {
		return s.fail(ctx, id, by, err)
	}
	ps, err := loadPromotions(ctx, s.store)
	if err != nil {
		return Promotion{}, err
	}
	i := slices.IndexFunc(ps, func(p Promotion) bool { return p.ID == id })
	if i < 0 {
		return Promotion{}, ErrNotFound
	}
	p := ps[i]
	msg := fmt.Sprintf("Promoted from %s version %d", p.FromEnv, p.FromVersion)

	var previous, applied int
	var errorsAt, processedAt uint64
	existing, getErr := s.store.GetWorkflow(ctx, wf.ID)
	switch {
	case getErr == nil:
		after := wf
		// This instance's run and placement stay as they are.
		after.VHost, after.WorkspaceID = existing.VHost, existing.WorkspaceID
		after.Status, after.WorkerID, after.OwnerID, after.LeaseUntil = existing.Status, existing.WorkerID, existing.OwnerID, existing.LeaseUntil
		after.TotalProcessed, after.TotalErrors, after.TotalLag = existing.TotalProcessed, existing.TotalErrors, existing.TotalLag
		after.Active = existing.Active || p.Start
		errorsAt, processedAt = existing.TotalErrors, existing.TotalProcessed
		previous, applied, err = s.applier.ApplyWorkflowChange(ctx, existing, after, by, msg)
	case errors.Is(getErr, storage.ErrNotFound):
		wf.Active = p.Start
		wf.WorkspaceID = ""
		applied, err = s.applier.CreateWorkflowVersioned(ctx, wf, by, msg)
	default:
		err = getErr
	}
	if err != nil {
		return s.fail(ctx, id, by, err)
	}
	return s.update(ctx, id, func(p *Promotion) error {
		p.Status = StatusDeployed
		p.DecidedBy = by
		if p.DecidedAt.IsZero() {
			p.DecidedAt = s.now().UTC()
		}
		p.PreviousVersion, p.AppliedVersion = previous, applied
		p.DeployedAt = s.now().UTC()
		p.ErrorsAtDeploy, p.ProcessedAtDeploy = errorsAt, processedAt
		p.Reason = ""
		return nil
	})
}

func (s *Service) fail(ctx context.Context, id, by string, cause error) (Promotion, error) {
	p, err := s.update(ctx, id, func(p *Promotion) error {
		p.Status = StatusFailed
		p.DecidedBy = by
		p.Reason = cause.Error()
		return nil
	})
	if err != nil {
		return p, err
	}
	return p, fmt.Errorf("the promotion could not be deployed: %w", cause)
}

// Reject declines a pending promotion.
func (s *Service) Reject(ctx context.Context, id, username, reason string) (Promotion, error) {
	if len(reason) > 1000 {
		return Promotion{}, fmt.Errorf("%w: the reason must be at most 1000 characters", ErrInvalid)
	}
	return s.update(ctx, id, func(p *Promotion) error {
		if p.Status != StatusPending {
			return fmt.Errorf("%w: this promotion is %s, not pending", ErrConflict, p.Status)
		}
		p.Status = StatusRejected
		p.DecidedBy, p.DecidedAt = username, s.now().UTC()
		p.Reason = strings.TrimSpace(reason)
		return nil
	})
}

// Rollback undoes a deployed promotion: the workflow goes back to the
// version it had before, or is stopped when the promotion created it. Only
// the newest deployed promotion of a workflow can be rolled back, so an
// older one never undoes a newer one's change.
func (s *Service) Rollback(ctx context.Context, id, username, reason string) (Promotion, error) {
	ps, err := loadPromotions(ctx, s.store)
	if err != nil {
		return Promotion{}, err
	}
	i := slices.IndexFunc(ps, func(p Promotion) bool { return p.ID == id })
	if i < 0 {
		return Promotion{}, ErrNotFound
	}
	p := ps[i]
	if p.Status != StatusDeployed {
		return Promotion{}, fmt.Errorf("%w: only a deployed promotion can be rolled back; this one is %s", ErrConflict, p.Status)
	}
	if slices.ContainsFunc(ps[i+1:], func(q Promotion) bool { return q.WorkflowID == p.WorkflowID && q.Status == StatusDeployed }) {
		return Promotion{}, fmt.Errorf("%w: a newer promotion of this workflow was deployed since; roll that one back instead", ErrConflict)
	}
	if p.PreviousVersion > 0 {
		_, err = s.applier.RollbackWorkflowTo(ctx, p.WorkflowID, p.PreviousVersion, username)
	} else {
		var wf storage.Workflow
		wf, err = s.store.GetWorkflow(ctx, p.WorkflowID)
		if err == nil {
			after := wf
			after.Active = false
			_, _, err = s.applier.ApplyWorkflowChange(ctx, wf, after, username, "Stopped: its promotion was rolled back")
		}
	}
	if err != nil {
		return Promotion{}, fmt.Errorf("rolling back: %w", err)
	}
	if reason == "" {
		reason = "Rolled back by " + username
	}
	return s.update(ctx, id, func(p *Promotion) error {
		p.Status = StatusRolledBack
		p.Reason = reason
		return nil
	})
}

// failing reports why a deployed workflow counts as failing, or "": its
// engine reported an error, or at least 10 messages failed since the deploy
// and they are more than 5% of what it handled.
func failing(p Promotion, wf storage.Workflow) string {
	if strings.HasPrefix(wf.Status, "Error") {
		return "it stopped with " + wf.Status
	}
	var errs, done uint64
	if wf.TotalErrors > p.ErrorsAtDeploy {
		errs = wf.TotalErrors - p.ErrorsAtDeploy
	}
	if wf.TotalProcessed > p.ProcessedAtDeploy {
		done = wf.TotalProcessed - p.ProcessedAtDeploy
	}
	if errs >= 10 && errs*20 > done+errs {
		return fmt.Sprintf("%d of %d messages failed after the deploy", errs, done+errs)
	}
	return ""
}

// CheckDeploys rolls back every promotion that asked for automatic rollback,
// is still inside RollbackWindow, and is failing. It returns how many it
// rolled back.
func (s *Service) CheckDeploys(ctx context.Context) (int, error) {
	rules, err := loadRules(ctx, s.store)
	if err != nil || !rules.AutoRollback {
		return 0, err
	}
	ps, err := loadPromotions(ctx, s.store)
	if err != nil {
		return 0, err
	}
	now := s.now()
	n := 0
	for _, p := range ps {
		if p.Status != StatusDeployed || !p.AutoRollback || now.Sub(p.DeployedAt) > RollbackWindow {
			continue
		}
		wf, err := s.store.GetWorkflow(ctx, p.WorkflowID)
		if err != nil {
			continue
		}
		why := failing(p, wf)
		if why == "" {
			continue
		}
		if _, err := s.Rollback(ctx, p.ID, "auto-rollback", "Rolled back automatically: "+why); err != nil {
			slog.Warn("environment: automatic rollback failed", "promotion", p.ID, "workflow", p.WorkflowID, "error", err)
			continue
		}
		n++
	}
	return n, nil
}

// Watch runs CheckDeploys every interval until ctx ends.
func (s *Service) Watch(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := s.CheckDeploys(ctx); err != nil && ctx.Err() == nil {
				slog.Warn("environment: checking recent deploys failed", "error", err)
			}
		}
	}
}
