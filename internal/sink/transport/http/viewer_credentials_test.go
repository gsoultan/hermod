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
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/internal/testutil"
)

type viewerCredentialsStorage struct {
	testutil.BaseMockStorage
	item storage.Sink
}

func (s *viewerCredentialsStorage) ListSinks(context.Context, storage.CommonFilter) ([]storage.Sink, int, error) {
	return []storage.Sink{s.item}, 1, nil
}

func (s *viewerCredentialsStorage) GetSink(context.Context, string) (storage.Sink, error) {
	return s.item, nil
}

// A Viewer can open the sink list and a sink's page, and both answered with the
// stored password. Reading a connector does not need its credential; only
// the roles that edit it do.
func TestSinkCredentialsAreNotReturnedToAViewer(t *testing.T) {
	store := &viewerCredentialsStorage{item: storage.Sink{
		ID: "c1", Name: "orders", Type: "postgres", VHost: "sales",
		Config: map[string]string{"host": "db.internal", "password": "s3cret-value"},
	}}
	mux := http.NewServeMux()
	(&SinkHandler{Handler: &handlers.Handler{Storage: store}}).RegisterSinkRoutes(mux)

	call := func(role storage.Role, target string) string {
		t.Helper()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
		req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey,
			&storage.User{Role: role, VHosts: []string{"sales"}}))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s as %s answered %d: %s", target, role, rec.Code, rec.Body.String())
		}
		if !json.Valid(rec.Body.Bytes()) {
			t.Fatalf("%s as %s answered with a body that is not JSON: %s", target, role, rec.Body.String())
		}
		return rec.Body.String()
	}

	for _, target := range []string{"/api/sinks", "/api/sinks/c1"} {
		if body := call(storage.RoleViewer, target); strings.Contains(body, "s3cret-value") {
			t.Errorf("GET %s gave a Viewer the stored password: %s", target, body)
		} else if !strings.Contains(body, "db.internal") {
			t.Errorf("GET %s hid the host from a Viewer, which is not a credential: %s", target, body)
		}
		if body := call(storage.RoleEditor, target); !strings.Contains(body, "s3cret-value") {
			t.Errorf("GET %s did not give an Editor the password the edit form saves back: %s", target, body)
		}
	}
}
