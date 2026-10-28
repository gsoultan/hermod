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
	"slices"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/internal/storage/configsecrets"
)

// Everything lives in the settings table, as the self-healing proposals do,
// so no backend needs a migration.
const (
	keyInstance      = "hermod.environment"
	keyRules         = "hermod.environment_rules"
	keyLinks         = "hermod.environment_links"
	keyPeers         = "hermod.environment_peers"
	keyPairing       = "hermod.environment_pairing"
	keyPromotions    = "hermod.environment_promotions"
	keyPromotionBody = "hermod.environment_promotion:"
)

// maxPromotionsKept bounds the promotion history; the oldest decided ones go
// first, and pending ones are never dropped.
const maxPromotionsKept = 200

// Settings is the key-value store everything is kept in.
type Settings interface {
	GetSetting(ctx context.Context, key string) (string, error)
	SaveSetting(ctx context.Context, key, value string) error
}

// getJSON reads key into dst. A setting that was never written leaves dst
// as it is.
func getJSON(ctx context.Context, s Settings, key string, dst any) error {
	raw, err := s.GetSetting(ctx, key)
	if errors.Is(err, storage.ErrNotFound) || (err == nil && raw == "") {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading %s: %w", key, err)
	}
	if err := json.Unmarshal([]byte(raw), dst); err != nil {
		return fmt.Errorf("%s is unreadable: %w", key, err)
	}
	return nil
}

func putJSON(ctx context.Context, s Settings, key string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := s.SaveSetting(ctx, key, string(raw)); err != nil {
		return fmt.Errorf("saving %s: %w", key, err)
	}
	return nil
}

func loadInstance(ctx context.Context, s Settings) (Instance, error) {
	var in Instance
	err := getJSON(ctx, s, keyInstance, &in)
	return in, err
}

func loadRules(ctx context.Context, s Settings) (Rules, error) {
	r := DefaultRules()
	err := getJSON(ctx, s, keyRules, &r)
	return r, err
}

func loadLinks(ctx context.Context, s Settings) ([]Link, error) {
	var out []Link
	err := getJSON(ctx, s, keyLinks, &out)
	return out, err
}

func loadPeers(ctx context.Context, s Settings) ([]Peer, error) {
	var out []Peer
	err := getJSON(ctx, s, keyPeers, &out)
	return out, err
}

func loadPairing(ctx context.Context, s Settings) ([]PairingCode, error) {
	var out []PairingCode
	err := getJSON(ctx, s, keyPairing, &out)
	return out, err
}

func loadPromotions(ctx context.Context, s Settings) ([]Promotion, error) {
	var out []Promotion
	err := getJSON(ctx, s, keyPromotions, &out)
	return out, err
}

// savePromotions writes the history, dropping the oldest decided promotions
// past the cap together with the workflows they carried.
func savePromotions(ctx context.Context, s Settings, ps []Promotion) error {
	for len(ps) > maxPromotionsKept {
		i := slices.IndexFunc(ps, func(p Promotion) bool { return p.Status != StatusPending })
		if i < 0 {
			break
		}
		if err := s.SaveSetting(ctx, keyPromotionBody+ps[i].ID, ""); err != nil {
			return fmt.Errorf("dropping promotion %s: %w", ps[i].ID, err)
		}
		ps = slices.Delete(ps, i, i+1)
	}
	return putJSON(ctx, s, keyPromotions, ps)
}

func loadPromotionWorkflow(ctx context.Context, s Settings, id string) (storage.Workflow, error) {
	var wf storage.Workflow
	if err := getJSON(ctx, s, keyPromotionBody+id, &wf); err != nil {
		return wf, err
	}
	if wf.ID == "" {
		return wf, fmt.Errorf("promotion %s: %w", id, ErrNotFound)
	}
	return wf, nil
}

// linkTokenField is a link's key inside storage.EnvironmentLinkTokensKey.
// The _token suffix is what makes configsecrets encrypt it.
func linkTokenField(id string) string { return id + "_token" }

func loadLinkToken(ctx context.Context, s Settings, id string) (string, error) {
	stored := map[string]string{}
	if err := getJSON(ctx, s, storage.EnvironmentLinkTokensKey, &stored); err != nil {
		return "", err
	}
	return configsecrets.Decrypt(stored)[linkTokenField(id)], nil
}

// saveLinkToken sets or, with an empty token, removes a link's token. The
// other tokens are kept as stored, still encrypted.
func saveLinkToken(ctx context.Context, s Settings, id, token string) error {
	stored := map[string]string{}
	if err := getJSON(ctx, s, storage.EnvironmentLinkTokensKey, &stored); err != nil {
		return err
	}
	if token == "" {
		delete(stored, linkTokenField(id))
		return putJSON(ctx, s, storage.EnvironmentLinkTokensKey, stored)
	}
	enc := configsecrets.Encrypt(map[string]string{linkTokenField(id): token})
	v := enc[linkTokenField(id)]
	if !configsecrets.IsEncrypted(v) {
		// configsecrets drops a value it cannot encrypt rather than store it
		// in the clear; saving without it would quietly lose the link.
		return errors.New("the link's token could not be encrypted, so the link was not saved")
	}
	stored[linkTokenField(id)] = v
	return putJSON(ctx, s, storage.EnvironmentLinkTokensKey, stored)
}
