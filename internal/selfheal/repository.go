// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package selfheal

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
)

// keyPrefix names the setting holding a workflow's proposals. They live in
// the settings table so no migration is needed on any backend.
const keyPrefix = "self_healing_proposals:"

// maxKept bounds the proposals kept per workflow; the oldest decided ones go
// first, and pending ones are never dropped.
const maxKept = 50

// Settings is the key-value store proposals are kept in.
type Settings interface {
	GetSetting(ctx context.Context, key string) (string, error)
	SaveSetting(ctx context.Context, key, value string) error
}

func load(ctx context.Context, s Settings, workflowID string) ([]Proposal, error) {
	raw, err := s.GetSetting(ctx, keyPrefix+workflowID)
	if err != nil {
		return nil, fmt.Errorf("reading proposals: %w", err)
	}
	if raw == "" {
		return nil, nil
	}
	var out []Proposal
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("reading proposals: %w", err)
	}
	return out, nil
}

func save(ctx context.Context, s Settings, workflowID string, ps []Proposal) error {
	for len(ps) > maxKept {
		i := slices.IndexFunc(ps, func(p Proposal) bool { return p.Status != StatusPending })
		if i < 0 {
			break
		}
		ps = slices.Delete(ps, i, i+1)
	}
	raw, err := json.Marshal(ps)
	if err != nil {
		return err
	}
	if err := s.SaveSetting(ctx, keyPrefix+workflowID, string(raw)); err != nil {
		return fmt.Errorf("saving proposals: %w", err)
	}
	return nil
}
