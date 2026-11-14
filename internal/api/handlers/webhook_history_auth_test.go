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

// A webhook's address is public: the sender has no session, and the endpoint is
// held to its source's own API key or signature. Everything under
// /api/webhooks/ was let through on that reasoning, and the history of what
// those endpoints received is served from under the same prefix. So
// GET /api/webhooks/requests answered anyone who could reach the port with the
// headers and body of the last requests to every webhook.
func TestWebhookHistoryIsNotAPublicPath(t *testing.T) {
	reached := false
	guard := (&Handler{}).AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))

	for _, tc := range []struct {
		method, path string
		public       bool
	}{
		{http.MethodGet, "/api/webhooks/requests", false},
		{http.MethodPost, "/api/webhooks/requests/0618214e/replay", false},
		// Receiving is still public, including at a path that only looks
		// like the history's.
		{http.MethodPost, "/api/webhooks/orders", true},
		{http.MethodGet, "/api/webhooks/orders", true},
		{http.MethodPost, "/api/webhooks/requests/from-billing", true},
		{http.MethodPost, "/api/webhooks/requestsX", true},
	} {
		reached = false
		rec := httptest.NewRecorder()
		guard.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), tc.method, tc.path, nil))
		if tc.public && !reached {
			t.Errorf("%s %s answered %d without a session: a webhook's sender has none, so this must stay public",
				tc.method, tc.path, rec.Code)
		}
		if !tc.public && (reached || rec.Code != http.StatusUnauthorized) {
			t.Errorf("%s %s answered %d without a session (handler reached: %v), want 401: "+
				"it serves what webhooks received", tc.method, tc.path, rec.Code, reached)
		}
	}
}
