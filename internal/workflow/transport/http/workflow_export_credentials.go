// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package http

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/internal/storage/configsecrets"
	"github.com/gsoultan/hermod/internal/workflow/redact"
)

// An export file is downloaded, mailed and committed, so the connection
// credentials of its sources and sinks stay on the instance that wrote it.
// Each one is written as redact.Placeholder, which tells an import that a
// value was left out rather than empty. Importing the file where it came
// from fills it from that instance's own connection; importing it anywhere
// else needs it typed in, and the import says which ones.

// withheldCredential names one left-out value an import needs.
type withheldCredential struct {
	Kind  string `json:"kind"` // "source" or "sink"
	ID    string `json:"id"`
	Name  string `json:"name"`
	Field string `json:"field"`
}

// isConnectionCredential reports whether a connection setting holds a
// credential: a key the at-rest encryption treats as one, or an address with
// a password in it. An empty value and a {{secret.NAME}} or secret:NAME
// reference, which name a credential without holding it, are not.
func isConnectionCredential(key, value string) bool {
	v := strings.TrimSpace(value)
	if v == "" || v == redact.Placeholder || strings.Contains(v, "{{") || strings.HasPrefix(v, "secret:") {
		return false
	}
	if configsecrets.IsSensitive(key) {
		return true
	}
	u, err := url.Parse(v)
	if err != nil || u.User == nil {
		return false
	}
	_, hasPassword := u.User.Password()
	return hasPassword
}

// withoutCredentials returns cfg with every credential left out.
func withoutCredentials(cfg map[string]string) map[string]string {
	if cfg == nil {
		return nil
	}
	out := maps.Clone(cfg)
	for k, v := range out {
		if isConnectionCredential(k, v) {
			out[k] = redact.Placeholder
		}
	}
	return out
}

// withCredentials fills each left-out value in cfg from current, this
// instance's config for the same connection, and returns the keys it could
// not fill, sorted.
func withCredentials(cfg, current map[string]string) (map[string]string, []string) {
	if !slices.Contains(slices.Collect(maps.Values(cfg)), redact.Placeholder) {
		return cfg, nil
	}
	out := maps.Clone(cfg)
	var missing []string
	for k, v := range cfg {
		if v != redact.Placeholder {
			continue
		}
		if have, ok := current[k]; ok && have != redact.Placeholder {
			out[k] = have
			continue
		}
		missing = append(missing, k)
	}
	slices.Sort(missing)
	return out, missing
}

// restoreWithheldCredentials fills the bundle's left-out credentials from
// this instance and lists the ones it cannot fill. A source or sink is
// matched by ID, so a file imported where it was exported needs nothing typed.
func (h *WorkflowHandler) restoreWithheldCredentials(ctx context.Context, bundle *storage.WorkflowExportBundle) ([]withheldCredential, error) {
	var missing []withheldCredential
	for i, src := range bundle.Sources {
		var current map[string]string
		existing, err := h.Storage.GetSource(ctx, src.ID)
		switch {
		case err == nil:
			current = existing.Config
		case !errors.Is(err, storage.ErrNotFound):
			return nil, err
		}
		cfg, keys := withCredentials(src.Config, current)
		bundle.Sources[i].Config = cfg
		for _, k := range keys {
			missing = append(missing, withheldCredential{Kind: "source", ID: src.ID, Name: src.Name, Field: k})
		}
	}
	for i, snk := range bundle.Sinks {
		var current map[string]string
		existing, err := h.Storage.GetSink(ctx, snk.ID)
		switch {
		case err == nil:
			current = existing.Config
		case !errors.Is(err, storage.ErrNotFound):
			return nil, err
		}
		cfg, keys := withCredentials(snk.Config, current)
		bundle.Sinks[i].Config = cfg
		for _, k := range keys {
			missing = append(missing, withheldCredential{Kind: "sink", ID: snk.ID, Name: snk.Name, Field: k})
		}
	}
	return missing, nil
}

// writeMissingCredentials answers an import that cannot be saved until the
// named credentials are given.
func writeMissingCredentials(w http.ResponseWriter, missing []withheldCredential) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnprocessableEntity)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error":               "This file leaves out connection credentials this instance does not have. Enter them and import again.",
		"missing_credentials": missing,
	})
}
