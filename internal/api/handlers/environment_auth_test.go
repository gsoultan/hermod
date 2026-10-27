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
	"net/http"
	"net/http/httptest"
	"testing"
)

// A linked environment has no session: it calls with a peer token, or with
// a pairing code to get one, and the environment handler checks either. The
// session middleware must let those calls reach it, and only those.
func TestLinkedEnvironmentCallsSkipTheSession(t *testing.T) {
	withJWTConfig(t)
	for path, want := range map[string]bool{
		"/api/environments/pair":              true,
		"/api/environments/peer/info":         true,
		"/api/environments/peer/promotions":   true,
		"/api/environments/promotions":        false,
		"/api/environments/pairing-codes":     false,
		"/api/environments/links":             false,
		"/api/environments/peers":             false,
		"/api/environments/pairX":             false,
		"/api/environments/peerish/promotion": false,
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)
		guarded(t, &Handler{}).ServeHTTP(rec, req)
		if passed := rec.Code == http.StatusOK; passed != want {
			t.Errorf("%s without a session: got %d, want reached=%v", path, rec.Code, want)
		}
	}
}
