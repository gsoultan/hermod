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
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gsoultan/hermod/internal/storage"
)

// ReleaseItem is one workflow picked for a release; Version 0 is its newest.
type ReleaseItem struct {
	WorkflowID string `json:"workflow_id"`
	Version    int    `json:"version"`
}

// ReleaseGroup is the part of a release that goes through one link.
type ReleaseGroup struct {
	LinkID      string    `json:"link_id"`
	LinkName    string    `json:"link_name"`
	VHost       string    `json:"vhost"`
	TargetVHost string    `json:"target_vhost,omitempty"`
	Previews    []Preview `json:"previews"`
	Ready       bool      `json:"ready"`
}

// Unlinked is a picked workflow whose vhost promotes nowhere.
type Unlinked struct {
	WorkflowID   string `json:"workflow_id"`
	WorkflowName string `json:"workflow_name"`
	VHost        string `json:"vhost"`
}

// ReleasePreview is everything shown before a release is sent: the picked
// workflows grouped by where they go, each with its checks.
type ReleasePreview struct {
	Groups   []ReleaseGroup `json:"groups"`
	Unlinked []Unlinked     `json:"unlinked"`
	Ready    bool           `json:"ready"`
}

// SentRelease is what one link's instance received.
type SentRelease struct {
	LinkID     string      `json:"link_id"`
	LinkName   string      `json:"link_name"`
	ReleaseID  string      `json:"release_id"`
	Promotions []Promotion `json:"promotions"`
}

// plannedGroup is a ReleaseGroup with what is needed to send it.
type plannedGroup struct {
	ReleaseGroup
	link  Link
	token string
	out   []storage.Workflow
}

// checkItems refuses an empty or oversized release, or one naming a
// workflow twice.
func checkItems(items []ReleaseItem) error {
	if len(items) == 0 || len(items) > maxReleaseItems {
		return fmt.Errorf("%w: a release carries 1 to %d workflows", ErrInvalid, maxReleaseItems)
	}
	seen := map[string]bool{}
	for _, it := range items {
		if it.WorkflowID == "" || seen[it.WorkflowID] {
			return fmt.Errorf("%w: each workflow can be in a release once", ErrInvalid)
		}
		seen[it.WorkflowID] = true
	}
	return nil
}

// planner groups a release's workflows by link as they are previewed.
type planner struct {
	s       *Service
	links   []Link
	groups  []*plannedGroup
	byLink  map[string]*plannedGroup
	reached map[string]reached
}

// group returns l's group, reaching its instance the first time.
func (pl *planner) group(ctx context.Context, l Link) (*plannedGroup, error) {
	if g := pl.byLink[l.ID]; g != nil {
		return g, nil
	}
	_, token, err := pl.s.link(ctx, l.ID)
	if err != nil {
		return nil, err
	}
	pl.reached[l.ID] = pl.s.reach(ctx, l, token)
	g := &plannedGroup{link: l, token: token, ReleaseGroup: ReleaseGroup{
		LinkID: l.ID, LinkName: l.Name, VHost: l.VHost, TargetVHost: l.TargetVHost, Previews: []Preview{}, Ready: true}}
	pl.byLink[l.ID] = g
	pl.groups = append(pl.groups, g)
	return g, nil
}

// add previews one workflow, or reports it unlinked.
func (pl *planner) add(ctx context.Context, it ReleaseItem, pv *ReleasePreview) error {
	wf, err := pl.s.store.GetWorkflow(ctx, it.WorkflowID)
	if errors.Is(err, storage.ErrNotFound) {
		return fmt.Errorf("workflow %s: %w", it.WorkflowID, ErrNotFound)
	}
	if err != nil {
		return err
	}
	l, ok := pl.s.linkFor(pl.links, vhostOf(wf.VHost))
	if !ok {
		pv.Unlinked = append(pv.Unlinked, Unlinked{WorkflowID: wf.ID, WorkflowName: wf.Name, VHost: vhostOf(wf.VHost)})
		return nil
	}
	g, err := pl.group(ctx, l)
	if err != nil {
		return err
	}
	p, out, err := pl.s.prepareWith(ctx, l, g.token, pl.reached[l.ID], it.WorkflowID, it.Version)
	if err != nil {
		return err
	}
	g.Previews = append(g.Previews, p)
	g.out = append(g.out, out)
	g.Ready = g.Ready && p.Ready
	return nil
}

// planRelease previews every picked workflow against the instance it goes
// to, reaching each instance once.
func (s *Service) planRelease(ctx context.Context, items []ReleaseItem) (ReleasePreview, []*plannedGroup, error) {
	if err := checkItems(items); err != nil {
		return ReleasePreview{}, nil, err
	}
	links, err := loadLinks(ctx, s.store)
	if err != nil {
		return ReleasePreview{}, nil, err
	}
	pl := &planner{s: s, links: links, byLink: map[string]*plannedGroup{}, reached: map[string]reached{}}
	pv := ReleasePreview{Groups: []ReleaseGroup{}, Unlinked: []Unlinked{}}
	for _, it := range items {
		if err := pl.add(ctx, it, &pv); err != nil {
			return ReleasePreview{}, nil, err
		}
	}
	pv.Ready = len(pv.Unlinked) == 0 && len(pl.groups) > 0
	for _, g := range pl.groups {
		pv.Groups = append(pv.Groups, g.ReleaseGroup)
		pv.Ready = pv.Ready && g.Ready
	}
	return pv, pl.groups, nil
}

// PreviewRelease checks a release without sending it.
func (s *Service) PreviewRelease(ctx context.Context, items []ReleaseItem) (ReleasePreview, error) {
	pv, _, err := s.planRelease(ctx, items)
	return pv, err
}

// SendRelease sends the picked workflows in one click: one release to each
// instance they go to. It is refused, and nothing is sent, while any of them
// is not ready or promotes nowhere.
func (s *Service) SendRelease(ctx context.Context, items []ReleaseItem, sender string, opts PromoteOptions) ([]SentRelease, error) {
	if len(opts.Note) > 1000 {
		return nil, fmt.Errorf("%w: the note must be at most 1000 characters", ErrInvalid)
	}
	pv, groups, err := s.planRelease(ctx, items)
	if err != nil {
		return nil, err
	}
	if !pv.Ready {
		return nil, fmt.Errorf("%w: %s", ErrInvalid, notReady(pv))
	}
	out := make([]SentRelease, 0, len(groups))
	for _, g := range groups {
		sent, err := s.sendGroup(ctx, g, sender, opts)
		if err != nil {
			if len(out) > 0 {
				return out, fmt.Errorf("sent to %s, but not to %s: %w", names(out), g.LinkName, err)
			}
			return nil, err
		}
		out = append(out, sent)
	}
	return out, nil
}

// notReady says what keeps a release from being sent.
func notReady(pv ReleasePreview) string {
	var why []string
	for _, u := range pv.Unlinked {
		why = append(why, fmt.Sprintf("%s: vhost %s promotes nowhere", u.WorkflowName, u.VHost))
	}
	for _, g := range pv.Groups {
		for _, p := range g.Previews {
			for _, c := range p.Checks {
				if c.Blocking && !c.OK {
					why = append(why, p.WorkflowName+": "+c.Name+" "+c.Detail)
				}
			}
		}
	}
	return strings.Join(why, "; ")
}

func names(sent []SentRelease) string {
	out := make([]string, 0, len(sent))
	for _, r := range sent {
		out = append(out, r.LinkName)
	}
	return strings.Join(out, ", ")
}

// sendGroup sends one link's part of a release. An instance from before
// releases gets the workflows one at a time, as separate promotions.
func (s *Service) sendGroup(ctx context.Context, g *plannedGroup, sender string, opts PromoteOptions) (SentRelease, error) {
	rs := ReleaseSubmission{ReleaseID: uuid.NewString()}
	for i, p := range g.Previews {
		rs.Items = append(rs.Items, Submission{
			Workflow: g.out[i], FromVersion: p.FromVersion, FromVersionAt: p.FromVersionAt, Sender: sender,
			Note: strings.TrimSpace(opts.Note), Start: opts.Start, AutoRollback: opts.AutoRollback, ReleaseID: rs.ReleaseID,
		})
	}
	got, err := s.remote.SubmitRelease(ctx, g.link.URL, g.token, rs)
	var remote *RemoteError
	if errors.As(err, &remote) && (remote.Status == http.StatusNotFound || remote.Status == http.StatusMethodNotAllowed) {
		got, err = s.submitOneByOne(ctx, g, rs)
	}
	if err != nil {
		return SentRelease{}, err
	}
	for i := range got {
		got[i].PeerID, got[i].FromEnv = g.link.ID, g.link.Name
		got[i].ReleaseID = rs.ReleaseID
		if j := slices.IndexFunc(g.Previews, func(p Preview) bool { return p.WorkflowID == got[i].WorkflowID }); j >= 0 {
			got[i].VHost = g.Previews[j].VHost
		}
	}
	if err := s.recordSent(ctx, got...); err != nil {
		return SentRelease{}, err
	}
	return SentRelease{LinkID: g.link.ID, LinkName: g.link.Name, ReleaseID: rs.ReleaseID, Promotions: got}, nil
}

func (s *Service) submitOneByOne(ctx context.Context, g *plannedGroup, rs ReleaseSubmission) ([]Promotion, error) {
	var got []Promotion
	for _, sub := range rs.Items {
		p, err := s.remote.Submit(ctx, g.link.URL, g.token, sub)
		if err != nil {
			if len(got) > 0 {
				_ = s.recordSent(ctx, got...)
			}
			return nil, fmt.Errorf("%s took %d of %d workflows one by one, then: %w", g.LinkName, len(got), len(rs.Items), err)
		}
		got = append(got, p)
	}
	return got, nil
}

// ReadyWorkflow is a workflow whose newest version has not been sent yet.
type ReadyWorkflow struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	VHost           string    `json:"vhost"`
	Version         int       `json:"version"`
	VersionAt       time.Time `json:"version_at,omitzero"`
	LastSentVersion int       `json:"last_sent_version"`
	Active          bool      `json:"active"`
}

// ReadyLane is one link and what could go through it.
type ReadyLane struct {
	LinkID      string          `json:"link_id"`
	LinkName    string          `json:"link_name"`
	VHost       string          `json:"vhost"`
	TargetVHost string          `json:"target_vhost,omitempty"`
	Workflows   []ReadyWorkflow `json:"workflows"`
}

// maxReadyPerLane bounds the workflows listed for one link.
const maxReadyPerLane = 200

// Ready lists, for each link, the workflows in its vhost whose newest
// version has not been sent through it: changed since the last promotion,
// or never promoted.
func (s *Service) Ready(ctx context.Context) ([]ReadyLane, error) {
	links, err := loadLinks(ctx, s.store)
	if err != nil {
		return nil, err
	}
	var log []Promotion
	if err := getJSON(ctx, s.store, keySent, &log); err != nil {
		return nil, err
	}
	lanes := make([]ReadyLane, 0, len(links))
	for _, l := range links {
		lane, err := s.readyLane(ctx, l, links, log)
		if err != nil {
			return nil, err
		}
		lanes = append(lanes, lane)
	}
	slices.SortFunc(lanes, func(a, b ReadyLane) int { return strings.Compare(strings.ToLower(a.VHost), strings.ToLower(b.VHost)) })
	return lanes, nil
}

// readyLane lists what could go through l.
func (s *Service) readyLane(ctx context.Context, l Link, links []Link, log []Promotion) (ReadyLane, error) {
	lane := ReadyLane{LinkID: l.ID, LinkName: l.Name, VHost: l.VHost, TargetVHost: l.TargetVHost, Workflows: []ReadyWorkflow{}}
	// Workflows saved with no vhost are in default, so default lists all.
	filter := l.VHost
	if filter == "default" {
		filter = ""
	}
	wfs, _, err := s.store.ListWorkflows(ctx, storage.CommonFilter{VHost: filter, Limit: maxListed})
	if err != nil {
		return lane, fmt.Errorf("listing workflows: %w", err)
	}
	for _, wf := range wfs {
		if l.VHost != "" && vhostOf(wf.VHost) != l.VHost {
			continue
		}
		// A link from before vhosts serves those with no link of their own.
		if own, ok := s.linkFor(links, vhostOf(wf.VHost)); l.VHost == "" && (!ok || own.ID != l.ID) {
			continue
		}
		r, ok, err := s.readyWorkflow(ctx, wf, l.ID, log)
		if err != nil {
			return lane, err
		}
		if ok && len(lane.Workflows) < maxReadyPerLane {
			lane.Workflows = append(lane.Workflows, r)
		}
	}
	slices.SortFunc(lane.Workflows, func(a, b ReadyWorkflow) int {
		return strings.Compare(strings.ToLower(a.Name)+a.ID, strings.ToLower(b.Name)+b.ID)
	})
	return lane, nil
}

// readyWorkflow reports wf when its newest version was never sent through
// linkID, or a rejected or failed send left it out.
func (s *Service) readyWorkflow(ctx context.Context, wf storage.Workflow, linkID string, log []Promotion) (ReadyWorkflow, bool, error) {
	r := ReadyWorkflow{ID: wf.ID, Name: wf.Name, VHost: vhostOf(wf.VHost), Active: wf.Active}
	versions, err := s.store.ListWorkflowVersions(ctx, wf.ID)
	if err != nil {
		return r, false, fmt.Errorf("listing versions of %s: %w", wf.Name, err)
	}
	if len(versions) > 0 {
		r.Version, r.VersionAt = versions[0].Version, versions[0].CreatedAt
	}
	for i := len(log) - 1; i >= 0; i-- {
		p := log[i]
		if p.PeerID != linkID || p.WorkflowID != wf.ID || p.Status == StatusRejected || p.Status == StatusFailed {
			continue
		}
		r.LastSentVersion = p.FromVersion
		break
	}
	sentBefore := slices.ContainsFunc(log, func(p Promotion) bool {
		return p.PeerID == linkID && p.WorkflowID == wf.ID && p.Status != StatusRejected && p.Status != StatusFailed
	})
	return r, !sentBefore || r.LastSentVersion != r.Version, nil
}
