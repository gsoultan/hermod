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
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gsoultan/hermod/internal/storage"
)

// maxCheckItems bounds each list in a CheckRequest.
const maxCheckItems = 500

// maxReleaseItems bounds how many workflows one release carries.
const maxReleaseItems = 50

// maxPeerPromotions bounds what a linked instance reads back.
const maxPeerPromotions = 100

// vhostOf names the vhost a workflow or connection is in; empty is default.
func vhostOf(v string) string {
	if v == "" {
		return "default"
	}
	return v
}

// mayUse reports whether peer may use something in vhost: a peer linked
// before links were per vhost may use anything.
func mayUse(peer Peer, vhost string) bool {
	return peer.VHost == "" || vhostOf(vhost) == vhostOf(peer.VHost)
}

// PeerCheck answers peer's CheckRequest, within the vhost it sends into:
// connections and workflows in other vhosts are reported missing, never
// described.
func (s *Service) PeerCheck(ctx context.Context, peer Peer, req CheckRequest) (CheckResult, error) {
	if len(req.Sources) > maxCheckItems || len(req.Sinks) > maxCheckItems ||
		len(req.Secrets) > maxCheckItems || len(req.Envs) > maxCheckItems {
		return CheckResult{}, fmt.Errorf("%w: too many items to check", ErrInvalid)
	}
	res := CheckResult{
		MissingSources: missing(req.Sources, func(id string) bool {
			src, err := s.store.GetSource(ctx, id)
			return err == nil && mayUse(peer, src.VHost)
		}),
		MissingSinks: missing(req.Sinks, func(id string) bool {
			snk, err := s.store.GetSink(ctx, id)
			return err == nil && mayUse(peer, snk.VHost)
		}),
	}
	vhost := req.VHost
	if peer.VHost != "" {
		vhost = peer.VHost
	}
	if req.WorkflowID != "" {
		if wf, err := s.store.GetWorkflow(ctx, req.WorkflowID); err == nil {
			if mayUse(peer, wf.VHost) {
				vhost = wf.VHost
				res.Current = s.current(ctx, wf)
			} else {
				res.Problem = "A workflow with the same ID is in another vhost there, which this link cannot send into."
			}
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

// admit checks a workflow peer sent and builds its promotion, without
// storing anything. It is checked here as well as on the sender: it lands in
// the vhost peer sends into, every connection it names must exist there, and
// the version must have soaked as long as that vhost's rules say. What it
// changes is worked out here, never taken from the sender.
func (s *Service) admit(ctx context.Context, peer Peer, sub Submission, rules Rules) (Promotion, storage.Workflow, error) {
	wf := sub.Workflow
	if err := validSubmission(sub); err != nil {
		return Promotion{}, wf, err
	}
	var current *storage.Workflow
	existing, err := s.store.GetWorkflow(ctx, wf.ID)
	switch {
	case err == nil:
		if !mayUse(peer, existing.VHost) {
			return Promotion{}, wf, fmt.Errorf("%w: workflow %s is in vhost %s here, which %s cannot send into", ErrForbidden, wf.ID, vhostOf(existing.VHost), peer.Name)
		}
		portable := s.applier.Portable(existing)
		current = &portable
		wf.VHost = existing.VHost
	case errors.Is(err, storage.ErrNotFound):
		if peer.VHost != "" {
			wf.VHost = peer.VHost
		}
	default:
		return Promotion{}, wf, fmt.Errorf("reading workflow %s: %w", wf.ID, err)
	}
	if why := soakProblem(rules.For(vhostOf(wf.VHost)), sub.FromVersionAt, s.now()); why != "" {
		return Promotion{}, wf, fmt.Errorf("%w: %s", ErrInvalid, why)
	}
	if err := s.hasConnections(ctx, peer, wf); err != nil {
		return Promotion{}, wf, err
	}
	wf = s.applier.Portable(wf)
	p := Promotion{
		ID: uuid.NewString(), PeerID: peer.ID, ReleaseID: sub.ReleaseID, FromEnv: peer.Name, WorkflowID: wf.ID, WorkflowName: wf.Name,
		VHost: wf.VHost, FromVersion: sub.FromVersion, Sender: strings.TrimSpace(sub.Sender),
		Note: strings.TrimSpace(sub.Note), Start: sub.Start, AutoRollback: sub.AutoRollback,
		Changes: Diff(current, wf), Status: StatusPending, CreatedAt: s.now().UTC(),
	}
	return p, wf, nil
}

// store keeps promotions and the workflows they carry, under the lock.
func (s *Service) storePromotions(ctx context.Context, ps []Promotion, wfs []storage.Workflow) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := loadPromotions(ctx, s.store)
	if err != nil {
		return err
	}
	for i, p := range ps {
		if err := putJSON(ctx, s.store, keyPromotionBody+p.ID, wfs[i]); err != nil {
			return err
		}
	}
	return savePromotions(ctx, s.store, append(all, ps...))
}

// Receive records a promotion peer sent. Under the "none" approval rule it
// is deployed at once.
func (s *Service) Receive(ctx context.Context, peer Peer, sub Submission) (Promotion, error) {
	if sub.ReleaseID != "" {
		if err := validReleaseID(sub.ReleaseID); err != nil {
			return Promotion{}, err
		}
	}
	rules, err := loadRules(ctx, s.store)
	if err != nil {
		return Promotion{}, err
	}
	p, wf, err := s.admit(ctx, peer, sub, rules)
	if err != nil {
		return Promotion{}, err
	}
	if err := s.storePromotions(ctx, []Promotion{p}, []storage.Workflow{wf}); err != nil {
		return Promotion{}, err
	}
	if rules.For(vhostOf(p.VHost)).Approval == ApproveNone {
		return s.deployGroup(ctx, p.ID, "automatic")
	}
	return p, nil
}

var releaseIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

func validReleaseID(id string) error {
	if !releaseIDPattern.MatchString(id) {
		return fmt.Errorf("%w: a release needs an ID of at most 64 letters, digits and dashes", ErrInvalid)
	}
	return nil
}

// ReceiveRelease records the workflows peer sent in one release. Every one
// is checked before any is stored, so a release arrives whole or not at
// all. Sending the same release again returns what the first one stored.
func (s *Service) ReceiveRelease(ctx context.Context, peer Peer, rs ReleaseSubmission) ([]Promotion, error) {
	if err := validReleaseID(rs.ReleaseID); err != nil {
		return nil, err
	}
	if len(rs.Items) == 0 || len(rs.Items) > maxReleaseItems {
		return nil, fmt.Errorf("%w: a release carries 1 to %d workflows", ErrInvalid, maxReleaseItems)
	}
	if got, err := s.release(ctx, peer.ID, rs.ReleaseID); err != nil || len(got) > 0 {
		return got, err
	}
	rules, err := loadRules(ctx, s.store)
	if err != nil {
		return nil, err
	}
	ps := make([]Promotion, 0, len(rs.Items))
	wfs := make([]storage.Workflow, 0, len(rs.Items))
	seen := map[string]bool{}
	for _, sub := range rs.Items {
		sub.ReleaseID = rs.ReleaseID
		if seen[sub.Workflow.ID] {
			return nil, fmt.Errorf("%w: workflow %s is in the release twice", ErrInvalid, sub.Workflow.ID)
		}
		seen[sub.Workflow.ID] = true
		p, wf, err := s.admit(ctx, peer, sub, rules)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", cleanName(sub.Workflow.Name, sub.Workflow.ID), err)
		}
		ps, wfs = append(ps, p), append(wfs, wf)
	}
	if err := s.storePromotions(ctx, ps, wfs); err != nil {
		return nil, err
	}
	if rules.For(vhostOf(ps[0].VHost)).Approval == ApproveNone {
		if _, err := s.deployGroup(ctx, ps[0].ID, "automatic"); err != nil {
			slog.Warn("environment: deploying a release on arrival failed", "release", rs.ReleaseID, "error", err)
		}
	}
	return s.release(ctx, peer.ID, rs.ReleaseID)
}

// release returns the promotions of one release from peerID, in the order
// they were sent.
func (s *Service) release(ctx context.Context, peerID, releaseID string) ([]Promotion, error) {
	ps, err := loadPromotions(ctx, s.store)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(ps, func(p Promotion) bool { return p.PeerID != peerID || p.ReleaseID != releaseID }), nil
}

// PeerPromotions lists what peer sent here, newest first, as it stands.
func (s *Service) PeerPromotions(ctx context.Context, peer Peer) ([]Promotion, error) {
	ps, err := loadPromotions(ctx, s.store)
	if err != nil {
		return nil, err
	}
	ps = slices.DeleteFunc(ps, func(p Promotion) bool { return p.PeerID != peer.ID })
	slices.Reverse(ps)
	if len(ps) > maxPeerPromotions {
		ps = ps[:maxPeerPromotions]
	}
	return ps, nil
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

// hasConnections checks every connection wf names exists here, in the vhost
// peer sends into.
func (s *Service) hasConnections(ctx context.Context, peer Peer, wf storage.Workflow) error {
	srcIDs, sinkIDs := workflowRefs(wf)
	for _, id := range srcIDs {
		if src, err := s.store.GetSource(ctx, id); err != nil || !mayUse(peer, src.VHost) {
			return fmt.Errorf("%w: the workflow uses source %s, which this instance does not have in its vhost", ErrInvalid, id)
		}
	}
	for _, id := range sinkIDs {
		if snk, err := s.store.GetSink(ctx, id); err != nil || !mayUse(peer, snk.VHost) {
			return fmt.Errorf("%w: the workflow uses sink %s, which this instance does not have in its vhost", ErrInvalid, id)
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

// group is the indexes of the promotions approved, rejected and rolled
// back together with ps[i]: its release, or itself alone.
func group(ps []Promotion, i int) []int {
	if ps[i].ReleaseID == "" {
		return []int{i}
	}
	var out []int
	for j, p := range ps {
		if p.PeerID == ps[i].PeerID && p.ReleaseID == ps[i].ReleaseID {
			out = append(out, j)
		}
	}
	return out
}

func indexOf(ps []Promotion, id string) int {
	return slices.IndexFunc(ps, func(p Promotion) bool { return p.ID == id })
}

func (s *Service) promotion(ctx context.Context, id string) (Promotion, error) {
	ps, err := loadPromotions(ctx, s.store)
	if err != nil {
		return Promotion{}, err
	}
	i := indexOf(ps, id)
	if i < 0 {
		return Promotion{}, ErrNotFound
	}
	return ps[i], nil
}

// Approve deploys a pending promotion together with the rest of its
// release. Under the admin_except_sender rule the person who sent any of
// them cannot approve.
func (s *Service) Approve(ctx context.Context, id, username string) (Promotion, error) {
	rules, err := loadRules(ctx, s.store)
	if err != nil {
		return Promotion{}, err
	}
	// Claim them first, so two admins approving at once deploy them once.
	s.mu.Lock()
	ps, err := loadPromotions(ctx, s.store)
	if err != nil {
		s.mu.Unlock()
		return Promotion{}, err
	}
	i := indexOf(ps, id)
	if i < 0 {
		s.mu.Unlock()
		return Promotion{}, ErrNotFound
	}
	members := group(ps, i)
	for _, j := range members {
		p := ps[j]
		if p.Status != StatusPending {
			s.mu.Unlock()
			return Promotion{}, fmt.Errorf("%w: %s is %s, not pending", ErrConflict, p.WorkflowName, p.Status)
		}
		if rules.For(vhostOf(p.VHost)).Approval == ApproveAnyAdminExceptSender && strings.EqualFold(p.Sender, username) {
			s.mu.Unlock()
			return Promotion{}, fmt.Errorf("%w: you sent this, so another admin has to approve it", ErrForbidden)
		}
	}
	for _, j := range members {
		ps[j].Status = StatusDeploying
		ps[j].DecidedBy, ps[j].DecidedAt = username, s.now().UTC()
	}
	err = savePromotions(ctx, s.store, ps)
	s.mu.Unlock()
	if err != nil {
		return Promotion{}, err
	}
	return s.deployGroup(ctx, id, username)
}

// deployGroup deploys id's release in the order it was sent. When one fails
// the ones before it are undone, newest first, and the ones after it are
// not deployed: a release goes live whole or not at all. It returns id's
// promotion as it ends.
func (s *Service) deployGroup(ctx context.Context, id, by string) (Promotion, error) {
	ps, err := loadPromotions(ctx, s.store)
	if err != nil {
		return Promotion{}, err
	}
	i := indexOf(ps, id)
	if i < 0 {
		return Promotion{}, ErrNotFound
	}
	var ids []string
	for _, j := range group(ps, i) {
		ids = append(ids, ps[j].ID)
	}
	for k, member := range ids {
		failed, err := s.deploy(ctx, member, by)
		if err == nil {
			continue
		}
		if len(ids) > 1 {
			why := failed.WorkflowName + " in the same release could not be deployed"
			for j := k - 1; j >= 0; j-- {
				if uerr := s.undo(ctx, ids[j], by, "Undone: "+why); uerr != nil {
					slog.Warn("environment: undoing a release after a failed deploy", "promotion", ids[j], "error", uerr)
				}
			}
			for _, rest := range ids[k+1:] {
				_, _ = s.update(ctx, rest, func(p *Promotion) error {
					p.Status, p.DecidedBy, p.Reason = StatusFailed, by, "Not deployed: "+why
					return nil
				})
			}
		}
		got, gerr := s.promotion(ctx, id)
		if gerr != nil {
			return failed, err
		}
		return got, err
	}
	return s.promotion(ctx, id)
}

// deploy stores the promoted workflow through the workflow update path and
// records the outcome on the promotion.
func (s *Service) deploy(ctx context.Context, id, by string) (Promotion, error) {
	wf, err := loadPromotionWorkflow(ctx, s.store, id)
	if err != nil {
		return s.fail(ctx, id, by, err)
	}
	p, err := s.promotion(ctx, id)
	if err != nil {
		return Promotion{}, err
	}
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

// Reject declines a pending promotion together with the rest of its
// release.
func (s *Service) Reject(ctx context.Context, id, username, reason string) (Promotion, error) {
	if len(reason) > 1000 {
		return Promotion{}, fmt.Errorf("%w: the reason must be at most 1000 characters", ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ps, err := loadPromotions(ctx, s.store)
	if err != nil {
		return Promotion{}, err
	}
	i := indexOf(ps, id)
	if i < 0 {
		return Promotion{}, ErrNotFound
	}
	members := group(ps, i)
	for _, j := range members {
		if ps[j].Status != StatusPending {
			return Promotion{}, fmt.Errorf("%w: %s is %s, not pending", ErrConflict, ps[j].WorkflowName, ps[j].Status)
		}
	}
	for _, j := range members {
		ps[j].Status = StatusRejected
		ps[j].DecidedBy, ps[j].DecidedAt = username, s.now().UTC()
		ps[j].Reason = strings.TrimSpace(reason)
	}
	return ps[i], savePromotions(ctx, s.store, ps)
}

// Rollback undoes a deployed promotion together with the rest of its
// release, newest first: each workflow goes back to the version it had
// before, or is stopped when the promotion created it. Only the newest
// deployed promotion of a workflow can be rolled back, so an older one never
// undoes a newer one's change.
func (s *Service) Rollback(ctx context.Context, id, username, reason string) (Promotion, error) {
	ps, err := loadPromotions(ctx, s.store)
	if err != nil {
		return Promotion{}, err
	}
	i := indexOf(ps, id)
	if i < 0 {
		return Promotion{}, ErrNotFound
	}
	if ps[i].Status != StatusDeployed {
		return Promotion{}, fmt.Errorf("%w: only a deployed promotion can be rolled back; this one is %s", ErrConflict, ps[i].Status)
	}
	var deployed []int
	for _, j := range group(ps, i) {
		if ps[j].Status != StatusDeployed {
			continue
		}
		p := ps[j]
		if slices.ContainsFunc(ps[j+1:], func(q Promotion) bool {
			return q.WorkflowID == p.WorkflowID && q.Status == StatusDeployed && (q.ReleaseID == "" || q.ReleaseID != p.ReleaseID)
		}) {
			return Promotion{}, fmt.Errorf("%w: a newer promotion of %s was deployed since; roll that one back instead", ErrConflict, p.WorkflowName)
		}
		deployed = append(deployed, j)
	}
	if reason == "" {
		reason = "Rolled back by " + username
	}
	for k := len(deployed) - 1; k >= 0; k-- {
		if err := s.undo(ctx, ps[deployed[k]].ID, username, reason); err != nil {
			return Promotion{}, err
		}
	}
	return s.promotion(ctx, id)
}

// undo puts a deployed promotion's workflow back as it was and records it.
func (s *Service) undo(ctx context.Context, id, username, reason string) error {
	p, err := s.promotion(ctx, id)
	if err != nil {
		return err
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
		return fmt.Errorf("rolling back %s: %w", p.WorkflowName, err)
	}
	_, err = s.update(ctx, id, func(p *Promotion) error {
		p.Status = StatusRolledBack
		p.Reason = reason
		return nil
	})
	return err
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
	done := map[string]bool{}
	for i, p := range ps {
		if p.Status != StatusDeployed || !p.AutoRollback || now.Sub(p.DeployedAt) > RollbackWindow || done[p.ID] {
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
		members := group(ps, i)
		reason := "Rolled back automatically: " + why
		if len(members) > 1 {
			reason = "Rolled back automatically with its release: " + p.WorkflowName + " " + why
		}
		if _, err := s.Rollback(ctx, p.ID, "auto-rollback", reason); err != nil {
			slog.Warn("environment: automatic rollback failed", "promotion", p.ID, "workflow", p.WorkflowID, "error", err)
			continue
		}
		for _, j := range members {
			if ps[j].Status == StatusDeployed && !done[ps[j].ID] {
				done[ps[j].ID] = true
				n++
			}
		}
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
