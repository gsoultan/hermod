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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/advisor/dbdiag"
	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/engine/registry"
	"github.com/gsoultan/hermod/internal/storage"
)

func diagMux(t *testing.T, run func(ctx context.Context, src storage.Source) (dbdiag.Report, error)) *http.ServeMux {
	t.Helper()
	store := storeWith(t)
	if err := store.CreateSource(t.Context(), storage.Source{ID: "src-1", Name: "athena", Type: "postgres", VHost: "team-a",
		Config: map[string]string{"connection_string": "postgres://u:secret@db:6432/athena"}}); err != nil {
		t.Fatal(err)
	}
	reg := registry.NewRegistry(store)
	t.Cleanup(reg.Close)
	h := NewDiagnosisHandler(&handlers.Handler{Storage: store, LogStorage: store, Registry: reg})
	h.run = run
	mux := http.NewServeMux()
	h.RegisterDiagnosisRoutes(mux)
	return mux
}

func postDiag(t *testing.T, mux *http.ServeMux, id string, role storage.Role, vhosts ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/sources/"+id+"/diagnosis", nil)
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey,
		&storage.User{ID: "u-1", Username: "tester", Role: role, VHosts: vhosts}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestDiagnosisReturnsTheReport(t *testing.T) {
	mux := diagMux(t, func(context.Context, storage.Source) (dbdiag.Report, error) {
		return dbdiag.Report{Checks: []dbdiag.Check{{ID: "locks", Status: dbdiag.StatusOK}}}, nil
	})
	rec := postDiag(t, mux, "src-1", storage.RoleEditor, "team-a")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var got dbdiag.Report
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil || len(got.Checks) != 1 {
		t.Fatalf("report = %+v, %v", got, err)
	}
}

// It connects to the source's database, so it takes an editor of the
// source's vhost, like the lookup plan check.
func TestDiagnosisIsForEditorsOfTheSourcesVHost(t *testing.T) {
	ran := false
	mux := diagMux(t, func(context.Context, storage.Source) (dbdiag.Report, error) { ran = true; return dbdiag.Report{}, nil })
	if rec := postDiag(t, mux, "src-1", storage.RoleViewer, "team-a"); rec.Code != http.StatusForbidden {
		t.Errorf("viewer got %d", rec.Code)
	}
	if rec := postDiag(t, mux, "src-1", storage.RoleEditor, "team-b"); rec.Code != http.StatusForbidden {
		t.Errorf("editor of another vhost got %d", rec.Code)
	}
	if rec := postDiag(t, mux, "nope", storage.RoleEditor, "team-a"); rec.Code != http.StatusNotFound {
		t.Errorf("missing source got %d", rec.Code)
	}
	if ran {
		t.Error("the database was diagnosed for a caller who may not")
	}
}

// A database Hermod cannot reach is an answer, not a server error, and the
// connection string's password never reaches the response.
func TestDiagnosisUnreachableSaysSoWithoutTheCredentials(t *testing.T) {
	mux := diagMux(t, func(context.Context, storage.Source) (dbdiag.Report, error) {
		return dbdiag.Report{}, errors.New("could not reach the database: dial tcp db:6432: connection refused")
	})
	rec := postDiag(t, mux, "src-1", storage.RoleEditor, "team-a")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("the password leaked: %s", rec.Body)
	}
}
