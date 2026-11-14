// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package lookup

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
)

// Saving a workflow moves a typed username, password or header into the
// vhost's secrets and leaves {{secret.NAME}} in the step
// (internal/storage/nodeclean). That is only safe because api_lookup resolves
// those settings as templates for the message's vhost: this holds it to that.
func TestAPILookupSendsCredentialsMovedIntoVHostSecrets(t *testing.T) {
	t.Cleanup(func() { evaluator.SetSecretSource(nil) })
	evaluator.SetSecretSource(panmailScopedSecrets{
		"tenant-a/WF_1_LOOKUP_USERNAME":      "apiuser",
		"tenant-a/WF_1_LOOKUP_PASSWORD":      "pw-a",
		"tenant-a/WF_1_LOOKUP_AUTHORIZATION": "Token hdr-a",
	})
	var user, pass, hdr string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, _ = r.BasicAuth()
		hdr = r.Header.Get("X-Upstream-Auth")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	t.Cleanup(srv.Close)

	tr, reg := newAPIFixture()
	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetVHost("tenant-a")
	ctx := context.WithValue(t.Context(), hermod.RegistryKey, reg)
	if _, err := tr.Transform(ctx, msg, map[string]any{
		"method": "GET", "url": srv.URL + "/u", "targetField": "profile",
		"authType": "basic",
		"username": "{{secret.WF_1_LOOKUP_USERNAME}}",
		"password": "{{secret.WF_1_LOOKUP_PASSWORD}}",
		"headers":  `{"X-Upstream-Auth":"{{secret.WF_1_LOOKUP_AUTHORIZATION}}"}`,
	}); err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if user != "apiuser" || pass != "pw-a" || hdr != "Token hdr-a" {
		t.Fatalf("sent user %q pass %q header %q", user, pass, hdr)
	}
}
