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
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/internal/testutil"
)

type webhookHistoryStorage struct {
	testutil.BaseMockStorage
	sources  []storage.Source
	requests []storage.WebhookRequest
}

func (s *webhookHistoryStorage) ListSources(context.Context, storage.CommonFilter) ([]storage.Source, int, error) {
	return s.sources, len(s.sources), nil
}

func (s *webhookHistoryStorage) ListWebhookRequests(_ context.Context, f storage.WebhookRequestFilter) ([]storage.WebhookRequest, int, error) {
	var out []storage.WebhookRequest
	for _, r := range s.requests {
		if f.Path == "" || r.Path == f.Path {
			out = append(out, r)
		}
	}
	return out, len(out), nil
}

func (s *webhookHistoryStorage) GetWebhookRequest(_ context.Context, id string) (storage.WebhookRequest, error) {
	for _, r := range s.requests {
		if r.ID == id {
			return r, nil
		}
	}
	return storage.WebhookRequest{}, storage.ErrNotFound
}

func webhookHistoryMux() *http.ServeMux {
	store := &webhookHistoryStorage{
		sources: []storage.Source{
			{ID: "s-sales", Type: "webhook", VHost: "sales", Config: map[string]string{"path": "/api/webhooks/sales-in"}},
			{ID: "s-ops", Type: "webhook", VHost: "ops", Config: map[string]string{"path": "/api/webhooks/ops-in"}},
		},
		requests: []storage.WebhookRequest{
			{ID: "r-sales", Path: "/api/webhooks/sales-in", Method: "POST", Body: []byte(`{"card":"4111"}`)},
			{ID: "r-ops", Path: "/api/webhooks/ops-in", Method: "POST", Body: []byte(`{"disk":"full"}`)},
		},
	}
	mux := http.NewServeMux()
	(&SourceHandler{Handler: &handlers.Handler{Storage: store}}).RegisterSourceRoutes(mux)
	return mux
}

func webhookHistoryCall(t *testing.T, mux *http.ServeMux, user *storage.User, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, target, nil)
	if user != nil {
		req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, user))
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func webhookHistoryIDs(t *testing.T, rec *httptest.ResponseRecorder) []string {
	t.Helper()
	var body struct {
		Data []storage.WebhookRequest `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("history answered %d with a body that is not JSON: %s", rec.Code, rec.Body.String())
	}
	ids := make([]string, 0, len(body.Data))
	for _, r := range body.Data {
		ids = append(ids, r.ID)
	}
	return ids
}

// What a webhook received is the data of the vhost its source belongs to. The
// history was listed with no caller at all, and for a signed-in caller with no
// look at which vhosts they may see.
func TestWebhookHistoryIsScopedToTheCallersVHosts(t *testing.T) {
	mux := webhookHistoryMux()
	ops := &storage.User{Role: storage.RoleViewer, VHosts: []string{"ops"}}
	admin := &storage.User{Role: storage.RoleAdministrator}

	if rec := webhookHistoryCall(t, mux, nil, http.MethodGet, "/api/webhooks/requests"); rec.Code != http.StatusUnauthorized {
		t.Errorf("history with no caller answered %d, want 401: %s", rec.Code, rec.Body.String())
	}

	rec := webhookHistoryCall(t, mux, ops, http.MethodGet, "/api/webhooks/requests?path=/api/webhooks/ops-in")
	if ids := webhookHistoryIDs(t, rec); rec.Code != http.StatusOK || len(ids) != 1 || ids[0] != "r-ops" {
		t.Errorf("a viewer of ops asking for its own webhook got %d %v, want 200 [r-ops]", rec.Code, ids)
	}

	if rec := webhookHistoryCall(t, mux, ops, http.MethodGet, "/api/webhooks/requests?path=/api/webhooks/sales-in"); rec.Code != http.StatusForbidden {
		t.Errorf("a viewer of ops asking for a sales webhook got %d, want 403: %s", rec.Code, rec.Body.String())
	}

	// With no path named, only what the caller may see comes back.
	rec = webhookHistoryCall(t, mux, ops, http.MethodGet, "/api/webhooks/requests")
	if ids := webhookHistoryIDs(t, rec); rec.Code != http.StatusOK || len(ids) != 1 || ids[0] != "r-ops" {
		t.Errorf("a viewer of ops listing every webhook got %d %v, want 200 [r-ops]", rec.Code, ids)
	}

	rec = webhookHistoryCall(t, mux, admin, http.MethodGet, "/api/webhooks/requests")
	if ids := webhookHistoryIDs(t, rec); rec.Code != http.StatusOK || len(ids) != 2 {
		t.Errorf("an administrator listing every webhook got %d %v, want both", rec.Code, ids)
	}
}

// Replaying sends a stored request into its workflow again, so it is held to
// the same vhost as reading it. A request the caller may not see is answered
// as one that does not exist.
func TestWebhookReplayIsScopedToTheCallersVHosts(t *testing.T) {
	mux := webhookHistoryMux()
	ops := &storage.User{Role: storage.RoleEditor, VHosts: []string{"ops"}}

	rec := webhookHistoryCall(t, mux, ops, http.MethodPost, "/api/webhooks/requests/r-sales/replay")
	if rec.Code != http.StatusNotFound {
		t.Errorf("an editor of ops replaying a sales request got %d, want 404: %s", rec.Code, rec.Body.String())
	}
}
