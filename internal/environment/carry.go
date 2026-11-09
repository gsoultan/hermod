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
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/internal/storage/configsecrets"
)

// A source or sink a workflow uses that the receiving vhost has nothing to
// match is carried with the promotion and created there on approval, under
// the same ID, so the next promotion that uses it finds it. Only its
// settings travel: a credential must be a {{secret.NAME}} or {{env.NAME}}
// reference, whose value each environment keeps for itself. One typed in
// plain text keeps the connection where it is until it is moved to a secret.

// What a promotion does with each connection a workflow uses.
const (
	ConnUses      = "uses"      // one there replaces it
	ConnCreate    = "create"    // carried and created there on approval
	ConnBlocked   = "blocked"   // would be carried, but a credential is plain text
	ConnUnmatched = "unmatched" // a person has to pick its match there
)

// maxCarried bounds the connections one promotion carries.
const maxCarried = 50

// Setting is one of a carried connection's settings. A credential's value
// is shown only when it is a reference.
type Setting struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Secret bool   `json:"secret,omitempty"`
}

// ConnectionRow is one connection a workflow uses and what happens to it.
type ConnectionRow struct {
	Kind       string    `json:"kind"` // source or sink
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Type       string    `json:"type"`
	Status     string    `json:"status"`
	TargetID   string    `json:"target_id,omitempty"`
	TargetName string    `json:"target_name,omitempty"`
	Settings   []Setting `json:"settings,omitempty"`
	// PlainKeys are the credentials typed in plain text that keep a
	// blocked connection here.
	PlainKeys []string `json:"plain_keys,omitempty"`
}

// CarriedConnection is a source or sink as a promotion carries it.
type CarriedConnection struct {
	Kind   string            `json:"kind"` // source or sink
	ID     string            `json:"id"`
	Name   string            `json:"name"`
	Type   string            `json:"type"`
	Config map[string]string `json:"config,omitempty"`
}

var referencePattern = regexp.MustCompile(`\{\{\s*(secret|env)\.[^{}]+\}\}`)

// plainKeys lists the credentials in cfg that are typed in plain text
// rather than read from a secret or variable.
func plainKeys(cfg map[string]string) []string {
	var out []string
	for k, v := range cfg {
		if strings.TrimSpace(v) != "" && configsecrets.IsSensitive(k) && !referencePattern.MatchString(v) {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

// connSettings lists cfg for a person to check, hiding plain credentials.
func connSettings(cfg map[string]string) []Setting {
	out := make([]Setting, 0, len(cfg))
	for _, k := range slices.Sorted(maps.Keys(cfg)) {
		st := Setting{Key: k, Value: cfg[k], Secret: configsecrets.IsSensitive(k)}
		if st.Secret && !referencePattern.MatchString(st.Value) {
			st.Value = ""
		}
		out = append(out, st)
	}
	return out
}

// withSettings applies the values a person changed for the receiving
// instance to the carried connections. Only settings a connection has can
// change, and a credential only to a reference.
func withSettings(carried []CarriedConnection, settings map[string]map[string]string) ([]CarriedConnection, error) {
	out := make([]CarriedConnection, len(carried))
	for i, c := range carried {
		c.Config = maps.Clone(c.Config)
		for k, v := range settings[c.ID] {
			if _, ok := c.Config[k]; !ok {
				return nil, fmt.Errorf("%w: %s %s has no setting %s", ErrInvalid, c.Kind, c.Name, k)
			}
			c.Config[k] = v
		}
		if plain := plainKeys(c.Config); len(plain) > 0 {
			return nil, fmt.Errorf("%w: %s %s would carry %s in plain text; use a {{secret.NAME}}", ErrInvalid, c.Kind, c.Name, strings.Join(plain, ", "))
		}
		out[i] = c
	}
	for id := range settings {
		if !slices.ContainsFunc(carried, func(c CarriedConnection) bool { return c.ID == id }) {
			return nil, fmt.Errorf("%w: connection %s is not created by this promotion", ErrInvalid, id)
		}
	}
	return out, nil
}

// carriedSecrets maps each secret the carried connections read to them.
func carriedSecrets(carried []CarriedConnection) map[string][]string {
	uses := map[string][]string{}
	for _, c := range carried {
		for _, name := range configSecrets(c.Config) {
			uses[name] = append(uses[name], c.Kind+" "+c.Name)
		}
	}
	return uses
}

// carriedPlaces says where the connections a promotion brings use each
// secret.
func carriedPlaces(carried []CarriedConnection) places {
	where := places{}
	for _, c := range carried {
		where.merge(connectionPlaces(c.Kind, c.Name, c.Config))
	}
	return where
}

// admitCarried checks the connections a submission carries for wf, landing
// in vhost here, and returns the ones to create: one already here is used
// as it is.
func (s *Service) admitCarried(ctx context.Context, peer Peer, wf storage.Workflow, carried []CarriedConnection) ([]CarriedConnection, error) {
	if len(carried) > maxCarried {
		return nil, fmt.Errorf("%w: a promotion carries at most %d connections", ErrInvalid, maxCarried)
	}
	srcIDs, sinkIDs := workflowRefs(wf)
	var keep []CarriedConnection
	for _, c := range carried {
		var used bool
		switch c.Kind {
		case "source":
			used = slices.Contains(srcIDs, c.ID)
		case "sink":
			used = slices.Contains(sinkIDs, c.ID)
		default:
			return nil, fmt.Errorf("%w: a carried connection is a source or a sink", ErrInvalid)
		}
		if !used || c.ID == "" || len(c.ID) > 128 || strings.TrimSpace(c.Name) == "" || strings.TrimSpace(c.Type) == "" {
			return nil, fmt.Errorf("%w: carried %s %q is not one the workflow uses", ErrInvalid, c.Kind, c.Name)
		}
		if plain := plainKeys(c.Config); len(plain) > 0 {
			return nil, fmt.Errorf("%w: %s %s carries %s in plain text", ErrInvalid, c.Kind, c.Name, strings.Join(plain, ", "))
		}
		if vhost, found := s.connectionVHost(ctx, c.Kind, c.ID); found {
			if !mayUse(peer, vhost) {
				return nil, fmt.Errorf("%w: %s %s is in another vhost here", ErrForbidden, c.Kind, c.ID)
			}
			continue
		}
		keep = append(keep, c)
	}
	return keep, nil
}

// connectionVHost says where the source or sink id is here, if anywhere.
func (s *Service) connectionVHost(ctx context.Context, kind, id string) (string, bool) {
	if kind == "source" {
		src, err := s.store.GetSource(ctx, id)
		return src.VHost, err == nil
	}
	snk, err := s.store.GetSink(ctx, id)
	return snk.VHost, err == nil
}

// createCarried creates the connections p carries that are still missing,
// in the vhost the workflow lands in.
func (s *Service) createCarried(ctx context.Context, p Promotion, vhost string) error {
	for _, c := range p.Connections {
		if _, found := s.connectionVHost(ctx, c.Kind, c.ID); found {
			continue
		}
		var err error
		if c.Kind == "source" {
			err = s.store.CreateSource(ctx, storage.Source{ID: c.ID, Name: c.Name, Type: c.Type, VHost: vhost, Active: true, Config: c.Config})
		} else {
			err = s.store.CreateSink(ctx, storage.Sink{ID: c.ID, Name: c.Name, Type: c.Type, VHost: vhost, Active: true, Config: c.Config})
		}
		if err != nil {
			return fmt.Errorf("creating %s %s: %w", c.Kind, c.Name, err)
		}
	}
	return nil
}

// landsIn is the vhost wf is deployed into: its own here, else the one it
// was admitted into.
func (s *Service) landsIn(ctx context.Context, wf storage.Workflow) string {
	if existing, err := s.store.GetWorkflow(ctx, wf.ID); err == nil {
		return vhostOf(existing.VHost)
	}
	return vhostOf(wf.VHost)
}

// MoveToSecret moves a credential typed in plain text in one of this
// instance's sources or sinks into a vhost secret named name, and puts a
// {{secret.NAME}} reference in its place, so the connection can be carried.
// An existing secret of that name is never replaced.
func (s *Service) MoveToSecret(ctx context.Context, kind, id, key, name, by string) error {
	w, ok := s.secrets.(SecretWriter)
	if !ok {
		return fmt.Errorf("%w: this instance cannot store secrets; set one where its secret manager keeps them", ErrInvalid)
	}
	var vhost string
	var cfg map[string]string
	switch kind {
	case "source":
		src, err := s.store.GetSource(ctx, id)
		if err != nil {
			return err
		}
		vhost, cfg = src.VHost, src.Config
	case "sink":
		snk, err := s.store.GetSink(ctx, id)
		if err != nil {
			return err
		}
		vhost, cfg = snk.VHost, snk.Config
	default:
		return fmt.Errorf("%w: a connection is a source or a sink", ErrInvalid)
	}
	value, has := cfg[key]
	if !has || !slices.Contains(plainKeys(cfg), key) {
		return fmt.Errorf("%w: %s is not a credential typed in plain text", ErrInvalid, key)
	}
	if err := storage.ValidateVHostSecret(storage.VHostSecret{VHost: vhostOf(vhost), Name: name, Value: value}); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	created, err := w.CreateSecret(ctx, vhostOf(vhost), name, value, by)
	if err != nil {
		return err
	}
	if !created {
		return fmt.Errorf("%w: vhost %s already has a secret %s; pick another name", ErrConflict, vhostOf(vhost), name)
	}
	cfg = maps.Clone(cfg)
	cfg[key] = "{{secret." + name + "}}"
	if kind == "source" {
		src, _ := s.store.GetSource(ctx, id)
		src.Config = cfg
		return s.store.UpdateSource(ctx, src)
	}
	snk, _ := s.store.GetSink(ctx, id)
	snk.Config = cfg
	return s.store.UpdateSink(ctx, snk)
}
