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
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/selfupdate"
	"github.com/gsoultan/hermod/internal/selfupdate/install"
	"github.com/gsoultan/hermod/internal/storage"
	sqlstore "github.com/gsoultan/hermod/internal/storage/sql"
	_ "modernc.org/sqlite"
)

type auditStore struct {
	storage.Storage
	mu     sync.Mutex
	audits []storage.AuditLog
}

func (s *auditStore) CreateAuditLog(_ context.Context, l storage.AuditLog) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.audits = append(s.audits, l)
	return nil
}

type releases []selfupdate.Release

func (r releases) Releases(context.Context) ([]selfupdate.Release, error) { return r, nil }

func newAPI(t *testing.T, capability install.Capability) (*Handler, *auditStore, *[]install.Plan) {
	t.Helper()
	db, err := sql.Open("sqlite", fmt.Sprintf("file:updatesapi_%s?mode=memory&cache=shared", t.Name()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	st := sqlstore.NewSQLStorage(db, "sqlite")
	if err := st.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	store := &auditStore{Storage: st}
	base := handlers.Handler{Storage: store, LogStorage: store}
	tag := "v1.25.0"
	dl := "https://github.com/gsoultan/hermod/releases/download/" + tag + "/"
	var plans []install.Plan
	var mu sync.Mutex
	svc := selfupdate.New(selfupdate.Config{
		Current:    "v1.24.9 (Enterprise Edition)",
		Capability: capability,
		GOOS:       "linux", GOARCH: "amd64",
		Source: releases{{Tag: tag, Assets: []selfupdate.Asset{
			{Name: "checksums.txt", URL: dl + "checksums.txt"},
			{Name: install.ArchiveName(tag, "linux", "amd64"), URL: dl + install.ArchiveName(tag, "linux", "amd64")},
		}}},
		Install: func(_ context.Context, p install.Plan, _ string, _ func(install.Phase)) error {
			mu.Lock()
			plans = append(plans, p)
			mu.Unlock()
			return nil
		},
		Store: func() selfupdate.SettingsStore { return store },
	})
	t.Cleanup(svc.Wait)
	return NewHandler(&base, svc), store, &plans
}

var (
	admin  = &storage.User{ID: "u-admin", Username: "root", Role: storage.RoleAdministrator, VHosts: []string{"*"}}
	editor = &storage.User{ID: "u-ed", Username: "ada", Role: storage.RoleEditor, VHosts: []string{"*"}}
	viewer = &storage.User{ID: "u-vw", Username: "vic", Role: storage.RoleViewer, VHosts: []string{"*"}}
)

func call(h *Handler, user *storage.User, method, path, body string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	ctx := context.Background()
	if user != nil {
		ctx = context.WithValue(ctx, handlers.UserContextKey, user)
	}
	r := httptest.NewRequestWithContext(ctx, method, path, bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

var routes = []struct{ method, path, body string }{
	{http.MethodGet, "/api/system/updates", ""},
	{http.MethodPost, "/api/system/updates/check", ""},
	{http.MethodPost, "/api/system/updates/install", `{"version":"v1.25.0"}`},
	{http.MethodPut, "/api/system/updates/settings", `{"mode":"automatic"}`},
}

func TestOnlyAdministratorsReachUpdates(t *testing.T) {
	h, _, plans := newAPI(t, install.Capability{CanInstall: true})
	for _, rt := range routes {
		for _, u := range []*storage.User{nil, viewer, editor} {
			w := call(h, u, rt.method, rt.path, rt.body)
			if w.Code != http.StatusForbidden {
				t.Errorf("%s %s as %v = %d, want 403", rt.method, rt.path, u, w.Code)
			}
		}
	}
	if len(*plans) != 0 {
		t.Fatal("a non-administrator started an install")
	}
}

func TestStatusCheckAndInstall(t *testing.T) {
	h, store, plans := newAPI(t, install.Capability{CanInstall: true, Platform: "linux/amd64"})

	w := call(h, admin, http.MethodGet, "/api/system/updates", "")
	var st selfupdate.Status
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &st) != nil || st.Current != "v1.24.9" || st.Settings.Mode != selfupdate.ModeManual {
		t.Fatalf("GET = %d %s", w.Code, w.Body)
	}

	w = call(h, admin, http.MethodPost, "/api/system/updates/check", "")
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &st) != nil || !st.UpdateAvailable || st.Latest.Tag != "v1.25.0" {
		t.Fatalf("check = %d %s", w.Code, w.Body)
	}

	w = call(h, admin, http.MethodPost, "/api/system/updates/install", `{"version":"v1.25.0"}`)
	if w.Code != http.StatusAccepted {
		t.Fatalf("install = %d %s", w.Code, w.Body)
	}
	h.svc.Wait()
	if len(*plans) != 1 {
		t.Fatalf("installs = %d", len(*plans))
	}
	if !audited(store, "UPDATE_INSTALL") {
		t.Fatal("starting an install was not audited")
	}
}

func TestInstallRefusalsAreConflicts(t *testing.T) {
	h, _, _ := newAPI(t, install.Capability{CanInstall: true})
	for _, body := range []string{`{"version":"v1.24.0"}`, `{"version":""}`, `{`} {
		w := call(h, admin, http.MethodPost, "/api/system/updates/install", body)
		if w.Code != http.StatusBadRequest && w.Code != http.StatusConflict {
			t.Errorf("install %s = %d %s", body, w.Code, w.Body)
		}
	}
	blocked, _, _ := newAPI(t, install.Capability{Reason: "Hermod is running in a container"})
	w := call(blocked, admin, http.MethodPost, "/api/system/updates/install", `{"version":"v1.25.0"}`)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "container") {
		t.Fatalf("install in a container = %d %s", w.Code, w.Body)
	}
}

func TestSettingsAreValidatedSavedAndAudited(t *testing.T) {
	h, store, _ := newAPI(t, install.Capability{CanInstall: true})
	if w := call(h, admin, http.MethodPut, "/api/system/updates/settings", `{"mode":"sometimes"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("bad mode = %d", w.Code)
	}
	w := call(h, admin, http.MethodPut, "/api/system/updates/settings", `{"mode":"automatic","include_prereleases":true}`)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT = %d %s", w.Code, w.Body)
	}
	if got := h.svc.Settings(context.Background()); got.Mode != selfupdate.ModeAutomatic || !got.IncludePrereleases || got.CheckIntervalHours != 24 {
		t.Fatalf("saved = %+v", got)
	}
	if !audited(store, "UPDATE_SETTINGS") {
		t.Fatal("changing update settings was not audited")
	}
}

func TestWithoutUpdaterEveryRouteIsUnavailable(t *testing.T) {
	h := NewHandler(&handlers.Handler{}, nil)
	for _, rt := range routes {
		if w := call(h, admin, rt.method, rt.path, rt.body); w.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s = %d, want 503", rt.method, rt.path, w.Code)
		}
	}
}

func audited(s *auditStore, action string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.audits {
		if a.Action == action {
			return true
		}
	}
	return false
}
