// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package handlers

import (
	"net/http"
	"strings"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/internal/storage/configsecrets"
)

// RedactedCredential stands in for a credential the caller is not shown.
const RedactedCredential = redactedValue

// ConfigForCaller returns a connector's config as the caller may see it.
//
// A Viewer reads connectors and cannot change them, so nothing a Viewer does
// needs the credential, and the list and detail endpoints handed it over in
// the clear. For a Viewer every credential is replaced with
// RedactedCredential, in a copy: the stored config is left as it is. An empty
// value and a {{ }} reference to a vhost secret are kept, because neither is
// a credential and both say something a reader wants to know.
//
// Every other caller edits the connector, and the edit form saves back what
// it was given, so they get the config as stored.
func (h *Handler) ConfigForCaller(r *http.Request, config hermod.StringMap) hermod.StringMap {
	if role, _ := h.GetRoleAndVHosts(r); role != storage.RoleViewer {
		return config
	}
	if config == nil {
		return nil
	}
	shown := make(hermod.StringMap, len(config))
	for key, value := range config {
		if configsecrets.IsSensitive(key) && strings.TrimSpace(value) != "" && !strings.Contains(value, "{{") {
			value = RedactedCredential
		}
		shown[key] = value
	}
	return shown
}
