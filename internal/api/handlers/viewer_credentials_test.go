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
	"context"
	"net/http/httptest"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
)

// A Viewer reads connectors and cannot change them, so nothing they do needs
// the credential itself. The other roles edit the connector and get it as
// stored.
func TestConfigForCallerHidesCredentialsFromAViewerOnly(t *testing.T) {
	stored := hermod.StringMap{
		"host":          "db.internal",
		"password":      "s3cret",
		"api_key":       "sk-live-123",
		"sasl_password": "{{secret.kafka_password}}",
		"token":         "",
		"routing_key":   "orders.created",
	}
	h := &Handler{}
	as := func(role storage.Role) hermod.StringMap {
		req := httptest.NewRequestWithContext(t.Context(), "GET", "/api/sources/s1", nil)
		if role != "" {
			req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &storage.User{Role: role}))
		}
		return h.ConfigForCaller(req, stored)
	}

	got := as(storage.RoleViewer)
	for key, want := range map[string]string{
		"host":          "db.internal",               // not a credential
		"password":      RedactedCredential,          // a credential
		"api_key":       RedactedCredential,          // a credential
		"sasl_password": "{{secret.kafka_password}}", // names a secret, does not hold one
		"token":         "",                          // nothing to hide, and "not set" is worth seeing
		"routing_key":   "orders.created",            // routing, not authentication
	} {
		if got[key] != want {
			t.Errorf("a Viewer was given %s = %q, want %q", key, got[key], want)
		}
	}
	if stored["password"] != "s3cret" {
		t.Fatal("hiding the credential from a Viewer changed the stored config, which the caller still uses")
	}

	for _, role := range []storage.Role{storage.RoleAdministrator, storage.RoleEditor, ""} {
		if got := as(role); got["password"] != "s3cret" || got["api_key"] != "sk-live-123" {
			t.Errorf("role %q edits connectors and was given password = %q, api_key = %q", role, got["password"], got["api_key"])
		}
	}
}
