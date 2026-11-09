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
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/security/secrets"
)

const keySent = "hermod.environment_sent"

// maxSentKept bounds the record of promotions this instance sent.
const maxSentKept = 100

// Preview is everything shown before a promotion is sent.
type Preview struct {
	LinkID         string    `json:"link_id"`
	LinkName       string    `json:"link_name"`
	WorkflowID     string    `json:"workflow_id"`
	WorkflowName   string    `json:"workflow_name"`
	VHost          string    `json:"vhost"`
	FromVersion    int       `json:"from_version"`
	FromVersionAt  time.Time `json:"from_version_at,omitzero"`
	CurrentVersion int       `json:"current_version"`
	Checks         []Check   `json:"checks"`
	Changes        []Change  `json:"changes"`
	Rules          Rules     `json:"rules"`
	Ready          bool      `json:"ready"`
	// Unmatched are the connections the workflow uses that have no match
	// there yet, so they can be matched right where the problem shows.
	// Production's candidates are left out: editors see a preview, and the
	// matching screen that lists them is for administrators.
	Unmatched []MatchRow `json:"unmatched,omitempty"`
	// MissingEnvs are the server variables the workflow reads that are not
	// set there.
	MissingEnvs []string `json:"missing_envs,omitempty"`
	// MissingSecrets are the secrets the workflow names that the linked
	// instance lacks; the admin who approves it there fills in their values.
	MissingSecrets []string `json:"missing_secrets,omitempty"`
	// SecretUses names, for a missing secret a source or sink there reads,
	// each connection that reads it ("sink warehouse").
	SecretUses map[string][]string `json:"secret_uses,omitempty"`
	// SecretPlaces says, for each missing secret, every step, source and
	// sink that uses it, and in which setting.
	SecretPlaces map[string][]SecretPlace `json:"secret_places,omitempty"`
	// Connections are the sources and sinks the workflow uses, each with
	// what happens to it there.
	Connections []ConnectionRow `json:"connections,omitempty"`
	// carried are the ones created there on approval, as they are sent.
	carried []CarriedConnection
	// takesSecrets says the linked instance takes secret values.
	takesSecrets bool
}

// workflowRefs lists the sources and sinks a workflow names, in node order:
// a source or sink node's RefID, and the source a lookup node queries.
func workflowRefs(wf storage.Workflow) (sources, sinks []string) {
	for _, n := range wf.Nodes {
		switch n.Type {
		case "source":
			if n.RefID != "" && !slices.Contains(sources, n.RefID) {
				sources = append(sources, n.RefID)
			}
		case "sink":
			if n.RefID != "" && !slices.Contains(sinks, n.RefID) {
				sinks = append(sinks, n.RefID)
			}
		}
		for _, key := range storage.NodeConfigSourceKeys {
			if id, ok := n.Config[key].(string); ok && id != "" && !slices.Contains(sources, id) {
				sources = append(sources, id)
			}
		}
	}
	return sources, sinks
}

// rewriteRefs returns wf with its connection references replaced by the
// IDs in srcMap and sinkMap. The nodes and their settings are copied, never
// changed in place.
func rewriteRefs(wf storage.Workflow, srcMap, sinkMap map[string]string) storage.Workflow {
	out := wf
	out.Nodes = make([]storage.WorkflowNode, len(wf.Nodes))
	for i, n := range wf.Nodes {
		n.Config = maps.Clone(n.Config)
		switch n.Type {
		case "source":
			if id, ok := srcMap[n.RefID]; ok {
				n.RefID = id
			}
		case "sink":
			if id, ok := sinkMap[n.RefID]; ok {
				n.RefID = id
			}
		}
		for _, key := range storage.NodeConfigSourceKeys {
			if old, ok := n.Config[key].(string); ok {
				if id, ok := srcMap[old]; ok {
					n.Config[key] = id
				}
			}
		}
		out.Nodes[i] = n
	}
	return out
}

// references lists the secrets and env variables a workflow's nodes read,
// in every spelling the engine resolves: {{secret.NAME}}, secret("NAME"), a
// secret inside a function, and the rest (secrets.References). Checking only
// {{secret.NAME}} let a step's secret written by the value builder through
// unasked, and the workflow landed without it.
func references(wf storage.Workflow) (secretNames, envNames []string) {
	found := map[string]map[string]bool{"secret": {}, "env": {}}
	for _, n := range wf.Nodes {
		walkStrings(n.Config, func(v string) {
			for _, ref := range secrets.References(v) {
				found[ref.Kind][ref.Name] = true
			}
		})
	}
	return slices.Sorted(maps.Keys(found["secret"])), slices.Sorted(maps.Keys(found["env"]))
}

// configSecrets lists the secrets a source's or sink's settings read.
func configSecrets(cfg map[string]string) []string {
	var out []string
	for _, v := range cfg {
		for _, ref := range secrets.References(v) {
			if ref.Kind == "secret" && !slices.Contains(out, ref.Name) {
				out = append(out, ref.Name)
			}
		}
	}
	slices.Sort(out)
	return out
}

// walkStrings calls f with every string inside a decoded JSON value.
func walkStrings(v any, f func(string)) {
	switch x := v.(type) {
	case string:
		f(x)
	case map[string]any:
		for _, e := range x {
			walkStrings(e, f)
		}
	case []any:
		for _, e := range x {
			walkStrings(e, f)
		}
	}
}

// workflowAt returns the workflow as it was saved in version (the newest
// when 0), with the version's number and time. A workflow with no history
// is taken as it is stored, as version 0.
func (s *Service) workflowAt(ctx context.Context, id string, version int) (storage.Workflow, int, time.Time, error) {
	current, err := s.store.GetWorkflow(ctx, id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return storage.Workflow{}, 0, time.Time{}, ErrNotFound
		}
		return storage.Workflow{}, 0, time.Time{}, err
	}
	if version == 0 {
		versions, err := s.store.ListWorkflowVersions(ctx, id)
		if err != nil {
			return storage.Workflow{}, 0, time.Time{}, err
		}
		if len(versions) == 0 {
			return current, 0, time.Time{}, nil
		}
		version = versions[0].Version
	}
	v, err := s.store.GetWorkflowVersion(ctx, id, version)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return storage.Workflow{}, 0, time.Time{}, fmt.Errorf("version %d: %w", version, ErrNotFound)
		}
		return storage.Workflow{}, 0, time.Time{}, err
	}
	wf := current
	if v.Config != "" {
		if err := json.Unmarshal([]byte(v.Config), &wf); err != nil {
			return storage.Workflow{}, 0, time.Time{}, fmt.Errorf("version %d is unreadable: %w", version, err)
		}
	}
	wf.ID = id
	wf.Nodes, wf.Edges = v.Nodes, v.Edges
	wf.TraceRetention, wf.AuditRetention = v.TraceRetention, v.AuditRetention
	return wf, v.Version, v.CreatedAt, nil
}

// prepare builds the preview and the workflow as it would be sent. A
// linked instance that does not answer is a failed check, not an error, so
// the person sees it among the others.
func (s *Service) prepare(ctx context.Context, linkID, workflowID string, version int) (Preview, storage.Workflow, Link, string, error) {
	l, token, err := s.link(ctx, linkID)
	if err != nil {
		return Preview{}, storage.Workflow{}, Link{}, "", err
	}
	p, out, err := s.prepareWith(ctx, l, token, s.reach(ctx, l, token), workflowID, version)
	return p, out, l, token, err
}

// prepareWith is prepare for a link already reached, so a release asks each
// linked instance for its rules and connections once.
func (s *Service) prepareWith(ctx context.Context, l Link, token string, r reached, workflowID string, version int) (Preview, storage.Workflow, error) {
	wf, fromVersion, at, err := s.workflowAt(ctx, workflowID, version)
	if err != nil {
		return Preview{}, storage.Workflow{}, err
	}
	if l.VHost != "" && vhostOf(wf.VHost) != vhostOf(l.VHost) {
		return Preview{}, storage.Workflow{}, fmt.Errorf("%w: %s is in vhost %s, and the link to %s is for vhost %s", ErrInvalid, wf.Name, vhostOf(wf.VHost), l.Name, l.VHost)
	}
	p := Preview{
		LinkID: l.ID, LinkName: l.Name, WorkflowID: wf.ID, WorkflowName: wf.Name, VHost: wf.VHost,
		FromVersion: fromVersion, FromVersionAt: at, Checks: []Check{}, Changes: []Change{},
	}
	valid := Check{Name: "Workflow is valid", OK: true, Blocking: true}
	if verr := s.applier.Validate(ctx, wf); verr != nil {
		valid = Check{Name: valid.Name, Blocking: true, Detail: verr.Error()}
	}
	p.Checks = append(p.Checks, valid)
	out := s.checkAgainst(ctx, &p, l, token, r, wf, at)
	p.Ready = !slices.ContainsFunc(p.Checks, func(c Check) bool { return c.Blocking && !c.OK })
	return p, out, nil
}

// checkAgainst adds the checks that need the linked instance to p, and
// returns wf as it would land there; empty when the instance did not answer.
func (s *Service) checkAgainst(ctx context.Context, p *Preview, l Link, token string, r reached, wf storage.Workflow, at time.Time) storage.Workflow {
	if !r.check.OK {
		p.Checks = append(p.Checks, r.check)
		return storage.Workflow{}
	}
	p.Rules = r.info.Rules
	m := s.matchRefs(ctx, l, wf, r.targets, slices.Contains(r.info.Features, FeatureCarry))
	p.Checks = append(p.Checks, m.check)
	p.Unmatched = m.rows
	p.Connections, p.carried = m.conns, m.carried

	out := rewriteRefs(s.applier.Portable(wf), m.srcMap, m.sinkMap)
	secretNames, envNames := references(out)
	carriedUses := carriedSecrets(m.carried)
	for name := range carriedUses {
		if !slices.Contains(secretNames, name) {
			secretNames = append(secretNames, name)
		}
	}
	slices.Sort(secretNames)
	isCarried := func(id string) bool {
		return slices.ContainsFunc(m.carried, func(c CarriedConnection) bool { return c.ID == id })
	}
	req := CheckRequest{WorkflowID: wf.ID, VHost: wf.VHost, Secrets: secretNames, Envs: envNames,
		Sources: slices.DeleteFunc(slices.Sorted(maps.Values(m.srcMap)), isCarried),
		Sinks:   slices.DeleteFunc(slices.Sorted(maps.Values(m.sinkMap)), isCarried)}
	res, err := s.remote.Check(ctx, l.URL, token, req)
	if c := reachCheck(l, err); !c.OK {
		p.Checks = append(p.Checks, c)
		return storage.Workflow{}
	}
	if res.Problem != "" {
		p.Checks = append(p.Checks, Check{Name: "Can land in " + l.Name, Blocking: true, Detail: res.Problem})
	}
	if len(res.MissingSecrets) > 0 {
		p.MissingSecrets = res.MissingSecrets
		p.SecretUses = res.SecretUses
		where := stepPlaces(out)
		if len(res.SecretPlaces) > 0 {
			where.merge(res.SecretPlaces)
		} else {
			where.merge(placesFromUses(res.SecretUses))
		}
		where.merge(carriedPlaces(m.carried))
		p.SecretPlaces = where.only(res.MissingSecrets)
		for _, name := range res.MissingSecrets {
			if uses := carriedUses[name]; len(uses) > 0 {
				if p.SecretUses == nil {
					p.SecretUses = map[string][]string{}
				}
				p.SecretUses[name] = append(p.SecretUses[name], uses...)
			}
		}
	}
	p.takesSecrets = slices.Contains(r.info.Features, FeatureSecrets)
	p.Checks = append(p.Checks, presenceChecks(l, res, secretNames, envNames)...)
	p.MissingEnvs = res.MissingEnvs
	if soak := soakProblem(r.info.Rules, at, s.now()); soak != "" {
		p.Checks = append(p.Checks, Check{Name: "Has run long enough", Blocking: true, Detail: soak})
	} else if r.info.Rules.MinSoakMinutes > 0 {
		p.Checks = append(p.Checks, Check{Name: "Has run long enough", OK: true, Blocking: true})
	}

	var current *storage.Workflow
	if res.Current != nil {
		p.CurrentVersion = res.Current.Version
		current = &res.Current.Workflow
	}
	p.Changes = Diff(current, out)
	return out
}

// reached is what a linked instance said about itself, and whether it
// answered.
type reached struct {
	info    PeerInfo
	targets Connections
	check   Check
}

// reach asks the linked instance for its rules and connections.
func (s *Service) reach(ctx context.Context, l Link, token string) reached {
	var targets Connections
	info, err := s.remote.Info(ctx, l.URL, token)
	if err == nil {
		targets, err = s.remote.Connections(ctx, l.URL, token)
	}
	return reached{info: info, targets: targets, check: reachCheck(l, err)}
}

func reachCheck(l Link, err error) Check {
	c := Check{Name: l.Name + " is reachable", OK: err == nil, Blocking: true}
	if err != nil {
		c.Detail = err.Error()
	}
	return c
}

// matchRefs maps each connection wf uses to its match in the linked
// instance, and says which ones have none. With carry, one that has nothing
// to match there is carried and created there on approval, under its own
// ID, unless a credential in it is plain text.
func (s *Service) matchRefs(ctx context.Context, l Link, wf storage.Workflow, targets Connections, carry bool) (m matched) {
	srcIDs, sinkIDs := workflowRefs(wf)
	m.srcMap, m.sinkMap = map[string]string{}, map[string]string{}
	var unmatched, blocked []string
	matchAll := func(kind string, ids []string, get func(string) (Connection, map[string]string, error), candidates []Connection, into map[string]string) {
		for _, id := range ids {
			conn, cfg, err := get(id)
			if err != nil {
				unmatched = append(unmatched, fmt.Sprintf("%s %s (not found here)", kind, id))
				m.rows = append(m.rows, MatchRow{Kind: kind, ID: id, Name: id, Status: MatchMissing})
				m.conns = append(m.conns, ConnectionRow{Kind: kind, ID: id, Name: id, Status: ConnUnmatched})
				continue
			}
			row := ConnectionRow{Kind: kind, ID: conn.ID, Name: conn.Name, Type: conn.Type}
			t, status, manual := resolve(conn, l.Mapping, candidates)
			switch {
			case status == MatchMatched:
				into[id] = t.ID
				row.Status, row.TargetID, row.TargetName = ConnUses, t.ID, t.Name
			case status == MatchMissing && !manual && carry:
				row.Settings, row.PlainKeys = connSettings(cfg), plainKeys(cfg)
				if len(row.PlainKeys) > 0 {
					row.Status = ConnBlocked
					blocked = append(blocked, fmt.Sprintf("%s (%s typed in plain text)", conn.Name, strings.Join(row.PlainKeys, ", ")))
					break
				}
				row.Status = ConnCreate
				into[id] = id
				m.carried = append(m.carried, CarriedConnection{Kind: kind, ID: conn.ID, Name: conn.Name, Type: conn.Type, Config: maps.Clone(cfg)})
			default:
				row.Status = ConnUnmatched
				unmatched = append(unmatched, conn.Name)
				m.rows = append(m.rows, MatchRow{Kind: kind, ID: conn.ID, Name: conn.Name, Type: conn.Type, VHost: conn.VHost, Status: status})
			}
			m.conns = append(m.conns, row)
		}
	}
	matchAll("source", srcIDs, func(id string) (Connection, map[string]string, error) {
		x, err := s.store.GetSource(ctx, id)
		return Connection{ID: x.ID, Name: x.Name, Type: x.Type, VHost: x.VHost}, x.Config, err
	}, targets.Sources, m.srcMap)
	matchAll("sink", sinkIDs, func(id string) (Connection, map[string]string, error) {
		x, err := s.store.GetSink(ctx, id)
		return Connection{ID: x.ID, Name: x.Name, Type: x.Type, VHost: x.VHost}, x.Config, err
	}, targets.Sinks, m.sinkMap)

	m.check = connectionsCheck(l, len(srcIDs)+len(sinkIDs), len(m.carried), unmatched, blocked)
	return m
}

// connectionsCheck says whether every connection is ready: matched there,
// or carried there.
func connectionsCheck(l Link, total, carried int, unmatched, blocked []string) Check {
	var why []string
	if len(unmatched) > 0 {
		why = append(why, "Match these in Settings › Environments: "+strings.Join(unmatched, ", "))
	}
	if len(blocked) > 0 {
		why = append(why, "Move these credentials to a secret so the connection can be created there: "+strings.Join(blocked, "; "))
	}
	if len(why) > 0 {
		return Check{Name: "Connections matched", Blocking: true,
			Detail: fmt.Sprintf("%d of %d ready. %s", total-len(unmatched)-len(blocked), total, strings.Join(why, ". "))}
	}
	c := Check{Name: "Connections matched", OK: true, Blocking: true, Detail: fmt.Sprintf("%d of %d", total, total)}
	if carried > 0 {
		c.Detail = fmt.Sprintf("%d of %d; %d created in %s on approval", total-carried, total, carried, l.Name)
	}
	return c
}

// matched is what matchRefs found.
type matched struct {
	srcMap, sinkMap map[string]string
	check           Check
	rows            []MatchRow
	conns           []ConnectionRow
	carried         []CarriedConnection
}

// presenceChecks reports what the linked instance said it lacks.
func presenceChecks(l Link, res CheckResult, secretNames, envNames []string) []Check {
	var out []Check
	if missing := append(slices.Clone(res.MissingSources), res.MissingSinks...); len(missing) > 0 {
		out = append(out, Check{Name: "Connections exist in " + l.Name, Blocking: true, Detail: "Not found there: " + strings.Join(missing, ", ")})
	}
	if len(secretNames) > 0 {
		c := Check{Name: "Secrets exist in " + l.Name, OK: len(res.MissingSecrets) == 0}
		if !c.OK {
			c.Detail = "Not found in the vhost's secrets there: " + strings.Join(res.MissingSecrets, ", ") +
				". Only their names are sent; the admin who approves it there fills in the values (or leaves them blank when a secret manager serves them)."
		}
		out = append(out, c)
	}
	if len(envNames) > 0 {
		c := Check{Name: "Environment variables exist in " + l.Name, OK: len(res.MissingEnvs) == 0}
		if !c.OK {
			c.Detail = "Not set there: " + strings.Join(res.MissingEnvs, ", ")
		}
		out = append(out, c)
	}
	return out
}

// soakProblem says why a version saved at is too new for r, or "".
func soakProblem(r Rules, at, now time.Time) string {
	if r.MinSoakMinutes <= 0 {
		return ""
	}
	want := time.Duration(r.MinSoakMinutes) * time.Minute
	if at.IsZero() {
		return "This workflow has no saved version yet. Save it, let it run, then promote."
	}
	if age := now.Sub(at); age < want {
		return fmt.Sprintf("This version is %s old; the receiving instance wants %s.", age.Round(time.Minute), want)
	}
	return ""
}

// Preview checks a promotion without sending it.
func (s *Service) Preview(ctx context.Context, linkID, workflowID string, version int) (Preview, error) {
	p, _, _, _, err := s.prepare(ctx, linkID, workflowID, version)
	return p, err
}

// PromoteOptions are the sender's choices.
type PromoteOptions struct {
	Version      int    `json:"version"`
	Note         string `json:"note"`
	Start        bool   `json:"start"`
	AutoRollback bool   `json:"auto_rollback"`
	// SecretValues are values the sender typed for the receiving instance,
	// by name, for secrets the preview lists as missing there. They are sent
	// on and never kept here.
	SecretValues map[string]string `json:"secrets,omitempty"`
	// Settings change a carried connection's settings for the receiving
	// instance, by connection ID and setting: a host or database name that
	// differs there. A credential can only become a reference.
	Settings map[string]map[string]string `json:"settings,omitempty"`
}

// Promote sends a workflow version to a linked instance, after the same
// checks Preview shows. It is refused while a blocking check fails.
func (s *Service) Promote(ctx context.Context, linkID, workflowID, sender string, opts PromoteOptions) (Promotion, error) {
	if len(opts.Note) > 1000 {
		return Promotion{}, fmt.Errorf("%w: the note must be at most 1000 characters", ErrInvalid)
	}
	p, out, l, token, err := s.prepare(ctx, linkID, workflowID, opts.Version)
	if err != nil {
		return Promotion{}, err
	}
	if !p.Ready {
		var why []string
		for _, c := range p.Checks {
			if c.Blocking && !c.OK {
				why = append(why, c.Name+": "+c.Detail)
			}
		}
		return Promotion{}, fmt.Errorf("%w: %s", ErrInvalid, strings.Join(why, "; "))
	}
	if err := checkSecretNames([]Preview{p}, opts.SecretValues); err != nil {
		return Promotion{}, err
	}
	values, err := valuesFor([]Preview{p}, opts.SecretValues)
	if err != nil {
		return Promotion{}, err
	}
	carried, err := withSettings(p.carried, opts.Settings)
	if err != nil {
		return Promotion{}, err
	}
	sent, err := s.remote.Submit(ctx, l.URL, token, Submission{
		Workflow: out, FromVersion: p.FromVersion, FromVersionAt: p.FromVersionAt, Sender: sender,
		Note: strings.TrimSpace(opts.Note), Start: opts.Start, AutoRollback: opts.AutoRollback, Secrets: values[0],
		Connections: carried,
	})
	if err != nil {
		return Promotion{}, err
	}
	sent.PeerID, sent.FromEnv, sent.VHost = l.ID, l.Name, p.VHost
	return sent, s.recordSent(ctx, sent)
}

// recordSent adds what was sent to this instance's log of it. PeerID holds
// the link each went through and FromEnv its name.
func (s *Service) recordSent(ctx context.Context, sent ...Promotion) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var log []Promotion
	if err := getJSON(ctx, s.store, keySent, &log); err != nil {
		return err
	}
	log = append(log, sent...)
	if len(log) > maxSentKept {
		log = log[len(log)-maxSentKept:]
	}
	return putJSON(ctx, s.store, keySent, log)
}

// final statuses never change again, so a sent promotion in one is not
// asked about.
func final(status string) bool {
	return status == StatusRejected || status == StatusFailed || status == StatusRolledBack
}

// Sent lists the promotions this instance sent, newest first, each as the
// instance it went to last reported it. FromEnv holds that instance's name.
func (s *Service) Sent(ctx context.Context) ([]Promotion, error) {
	var log []Promotion
	if err := getJSON(ctx, s.store, keySent, &log); err != nil {
		return nil, err
	}
	if s.refreshSent(ctx, log) {
		s.mu.Lock()
		var fresh []Promotion
		err := getJSON(ctx, s.store, keySent, &fresh)
		if err == nil {
			for i := range fresh {
				if j := indexOf(log, fresh[i].ID); j >= 0 {
					fresh[i] = log[j]
				}
			}
			err = putJSON(ctx, s.store, keySent, fresh)
		}
		s.mu.Unlock()
		if err != nil {
			return nil, err
		}
	}
	slices.Reverse(log)
	return log, nil
}

// refreshSent asks each linked instance how the promotions still open there
// stand, and updates log in place. An instance that does not answer, or is
// too old to say, leaves its entries as they were.
func (s *Service) refreshSent(ctx context.Context, log []Promotion) bool {
	open := map[string]bool{}
	for _, p := range log {
		if !final(p.Status) {
			open[p.PeerID] = true
		}
	}
	changed := false
	for linkID := range open {
		l, token, err := s.link(ctx, linkID)
		if err != nil {
			continue
		}
		there, err := s.remote.Promotions(ctx, l.URL, token)
		if err != nil {
			continue
		}
		for _, t := range there {
			i := indexOf(log, t.ID)
			if i < 0 || log[i].Status == t.Status && log[i].Reason == t.Reason {
				continue
			}
			copyOutcome(&log[i], t)
			changed = true
		}
	}
	return changed
}

// ignoredSettings are workflow fields that describe this instance's run, or
// are compared on their own, rather than settings a promotion changes.
var ignoredSettings = map[string]bool{
	"id": true, "nodes": true, "edges": true, "active": true, "status": true, "worker_id": true,
	"owner_id": true, "lease_until": true, "total_processed": true, "total_errors": true,
	"total_lag": true, "workspace_id": true, "created_at": true, "updated_at": true,
}

func nodeLabel(n storage.WorkflowNode) string {
	for _, k := range []string{"label", "name"} {
		if v, ok := n.Config[k].(string); ok && strings.TrimSpace(v) != "" {
			return v
		}
	}
	return n.ID
}

func settingsOf(wf storage.Workflow) map[string]any {
	raw, _ := json.Marshal(wf)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	for k := range ignoredSettings {
		delete(out, k)
	}
	return out
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// Diff lists what changes when next replaces current (nil: next is new).
// Moving a node on the canvas is not a change.
func Diff(current *storage.Workflow, next storage.Workflow) []Change {
	if current == nil {
		return []Change{{Kind: "added", What: fmt.Sprintf("New workflow with %d nodes", len(next.Nodes))}}
	}
	out := nodeChanges(current.Nodes, next.Nodes)
	if c, ok := edgeChange(current.Edges, next.Edges); ok {
		out = append(out, c)
	}
	return append(out, settingChanges(settingsOf(*current), settingsOf(next))...)
}

func nodeChanges(was, now []storage.WorkflowNode) []Change {
	out := []Change{}
	old := map[string]storage.WorkflowNode{}
	for _, n := range was {
		old[n.ID] = n
	}
	seen := map[string]bool{}
	for _, n := range now {
		seen[n.ID] = true
		o, ok := old[n.ID]
		if !ok {
			out = append(out, Change{Kind: "added", NodeID: n.ID, What: fmt.Sprintf("%s node %s", n.Type, nodeLabel(n))})
		} else if keys := changedKeys(o, n); len(keys) > 0 {
			out = append(out, Change{Kind: "changed", NodeID: n.ID,
				What: fmt.Sprintf("%s node %s: %s", n.Type, nodeLabel(n), strings.Join(keys, ", "))})
		}
	}
	for _, n := range was {
		if !seen[n.ID] {
			out = append(out, Change{Kind: "removed", NodeID: n.ID, What: fmt.Sprintf("%s node %s", n.Type, nodeLabel(n))})
		}
	}
	return out
}

// changedKeys names what differs between two versions of a node, the first
// five and a count of the rest.
func changedKeys(o, n storage.WorkflowNode) []string {
	var keys []string
	if o.Type != n.Type || o.RefID != n.RefID {
		keys = append(keys, "connection")
	}
	for _, k := range slices.Sorted(maps.Keys(n.Config)) {
		if !jsonEqual(o.Config[k], n.Config[k]) {
			keys = append(keys, k)
		}
	}
	for _, k := range slices.Sorted(maps.Keys(o.Config)) {
		if _, ok := n.Config[k]; !ok {
			keys = append(keys, k)
		}
	}
	if !jsonEqual(o.UnitTests, n.UnitTests) {
		keys = append(keys, "unit tests")
	}
	if len(keys) > 5 {
		keys = append(keys[:5], fmt.Sprintf("%d more", len(keys)-5))
	}
	return keys
}

func edgeChange(was, now []storage.WorkflowEdge) (Change, bool) {
	edgeKey := func(e storage.WorkflowEdge) string {
		return e.SourceID + "\x00" + e.SourceHandle + "\x00" + e.TargetID + "\x00" + e.TargetHandle
	}
	oldEdges := map[string]bool{}
	for _, e := range was {
		oldEdges[edgeKey(e)] = true
	}
	added := 0
	for _, e := range now {
		if !oldEdges[edgeKey(e)] {
			added++
		}
		delete(oldEdges, edgeKey(e))
	}
	if added == 0 && len(oldEdges) == 0 {
		return Change{}, false
	}
	return Change{Kind: "changed", What: fmt.Sprintf("Links between nodes: %d added, %d removed", added, len(oldEdges))}, true
}

func settingChanges(was, now map[string]any) []Change {
	keys := slices.Sorted(maps.Keys(now))
	for k := range was {
		if _, ok := now[k]; !ok {
			keys = append(keys, k)
		}
	}
	var out []Change
	for _, k := range keys {
		if !jsonEqual(was[k], now[k]) {
			out = append(out, Change{Kind: "changed", What: "Setting " + k})
		}
	}
	return out
}
