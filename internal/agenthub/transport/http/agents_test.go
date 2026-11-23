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
	"testing"

	_ "modernc.org/sqlite"

	"github.com/gsoultan/hermod/internal/agenthub"
	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
	sqlstore "github.com/gsoultan/hermod/internal/storage/sql"
)

// ---------------------------------------------------------------------------
// Agents over the API: an Editor of a vhost adds an agent and is shown its
// token once, claims its devices and replaces or revokes the token; anyone
// with the vhost may see its agents; nobody else may do either.
// ---------------------------------------------------------------------------

type fullStore interface {
	storage.Storage
	storage.AgentStore
}

type apiStore struct {
	fullStore
	audits []storage.AuditLog
}

func (s *apiStore) CreateAuditLog(_ context.Context, l storage.AuditLog) error {
	s.audits = append(s.audits, l)
	return nil
}
func (s *apiStore) CreateLog(context.Context, storage.Log) error { return nil }

func newAPI(t *testing.T) (*Handler, *apiStore) {
	t.Helper()
	db, err := sql.Open("sqlite", fmt.Sprintf("file:agentapi_%s?mode=memory&cache=shared", t.Name()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	st := sqlstore.NewSQLStorage(db, "sqlite")
	if err := st.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	store := &apiStore{fullStore: st.(fullStore)}
	hub := agenthub.New(func() any { return store }, nil)
	return NewHandler(&handlers.Handler{Storage: store, LogStorage: store}, hub), store
}

var (
	admin   = &storage.User{ID: "u-admin", Username: "root", Role: storage.RoleAdministrator, VHosts: []string{"*"}}
	editorA = &storage.User{ID: "u-ed-a", Username: "ada", Role: storage.RoleEditor, VHosts: []string{"plant"}}
	editorB = &storage.User{ID: "u-ed-b", Username: "bob", Role: storage.RoleEditor, VHosts: []string{"office"}}
	viewerA = &storage.User{ID: "u-vw-a", Username: "vic", Role: storage.RoleViewer, VHosts: []string{"plant"}}
)

func call(h http.HandlerFunc, user *storage.User, method string, path map[string]string, body string) *httptest.ResponseRecorder {
	ctx := context.Background()
	if user != nil {
		ctx = context.WithValue(ctx, handlers.UserContextKey, user)
	}
	r := httptest.NewRequestWithContext(ctx, method, "/api/agents", bytes.NewBufferString(body))
	for k, v := range path {
		r.SetPathValue(k, v)
	}
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

func create(t *testing.T, h *Handler, user *storage.User, vhost, name string) createResponse {
	t.Helper()
	w := call(h.CreateAgent, user, http.MethodPost, map[string]string{"vhost": vhost}, `{"name":"`+name+`"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST = %d %s", w.Code, w.Body)
	}
	var out createResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestAnEditorAddsAnAgentAndIsShownItsTokenOnce(t *testing.T) {
	h, store := newAPI(t)
	out := create(t, h, editorA, "plant", "Line 1")
	if out.Agent.ID == "" || out.Agent.VHost != "plant" || out.Agent.Name != "Line 1" || out.Agent.CreatedBy != "ada" {
		t.Fatalf("agent = %+v", out.Agent)
	}
	if id, ok := storage.AgentIDFromToken(out.Token); !ok || id != out.Agent.ID {
		t.Fatalf("token %q does not name agent %q", out.Token, out.Agent.ID)
	}
	saved, err := store.GetAgent(t.Context(), out.Agent.ID)
	if err != nil || saved.TokenHash != storage.HashAgentToken(out.Token) {
		t.Fatalf("stored = %+v, %v", saved, err)
	}

	w := call(h.GetAgent, viewerA, http.MethodGet, map[string]string{"id": out.Agent.ID}, "")
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), out.Token) ||
		strings.Contains(w.Body.String(), saved.TokenHash) {
		t.Fatalf("GET = %d %s: the token or its hash is shown again", w.Code, w.Body)
	}
	if len(store.audits) != 1 || store.audits[0].EntityID != out.Agent.ID || store.audits[0].EntityType != "agent" ||
		store.audits[0].VHost != "plant" || strings.Contains(store.audits[0].Payload, out.Token) {
		t.Fatalf("audits = %+v", store.audits)
	}
}

func TestDevicesAreClaimedAndValidated(t *testing.T) {
	h, store := newAPI(t)
	out := create(t, h, editorA, "plant", "Line 1")
	id := map[string]string{"id": out.Agent.ID}
	body := `{"name":"Line one","devices":[{"device_id":"usb:0403:6001:A1","kind":"serial","name":"scale",
		"enabled":true,"serial":{"baud_rate":9600},"framing":{"kind":"delimiter","delimiter_hex":"0d0a"}}]}`
	w := call(h.UpdateAgent, editorA, http.MethodPut, id, body)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT = %d %s", w.Code, w.Body)
	}
	saved, _ := store.GetAgent(t.Context(), out.Agent.ID)
	if saved.Name != "Line one" || len(saved.Devices) != 1 || saved.Devices[0].Framing.DelimiterHex != "0d0a" {
		t.Fatalf("stored = %+v", saved)
	}
	bad := `{"name":"x","devices":[{"device_id":"d","kind":"serial","serial":{"baud_rate":0}}]}`
	if w := call(h.UpdateAgent, editorA, http.MethodPut, id, bad); w.Code != http.StatusBadRequest {
		t.Fatalf("invalid settings: %d %s", w.Code, w.Body)
	}
}

func TestReplacingTheTokenRevokesTheOldOne(t *testing.T) {
	h, store := newAPI(t)
	out := create(t, h, editorA, "plant", "Line 1")
	w := call(h.ReplaceToken, editorA, http.MethodPost, map[string]string{"id": out.Agent.ID}, "")
	if w.Code != http.StatusOK {
		t.Fatalf("POST token = %d %s", w.Code, w.Body)
	}
	var tok struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &tok)
	saved, _ := store.GetAgent(t.Context(), out.Agent.ID)
	if tok.Token == "" || tok.Token == out.Token || saved.TokenHash != storage.HashAgentToken(tok.Token) {
		t.Fatalf("new token %q, stored hash %q", tok.Token, saved.TokenHash)
	}
}

func TestAgentsAreListedPerVHostAndDeleted(t *testing.T) {
	h, store := newAPI(t)
	a := create(t, h, editorA, "plant", "A")
	create(t, h, editorB, "office", "B")
	w := call(h.ListAgents, viewerA, http.MethodGet, map[string]string{"vhost": "plant"}, "")
	var list []agentView
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || len(list) != 1 || list[0].Agent.Name != "A" {
		t.Fatalf("list = %d %s", w.Code, w.Body)
	}
	if w := call(h.DeleteAgent, editorA, http.MethodDelete, map[string]string{"id": a.Agent.ID}, ""); w.Code != http.StatusNoContent {
		t.Fatalf("DELETE = %d %s", w.Code, w.Body)
	}
	if _, err := store.GetAgent(t.Context(), a.Agent.ID); err == nil {
		t.Fatal("the agent is still stored")
	}
}

func TestAgentAccess(t *testing.T) {
	h, _ := newAPI(t)
	a := create(t, h, editorA, "plant", "A")
	id := map[string]string{"id": a.Agent.ID}
	plant := map[string]string{"vhost": "plant"}
	cases := []struct {
		name string
		fn   http.HandlerFunc
		user *storage.User
		path map[string]string
		body string
		want int
	}{
		{"admin lists any vhost", h.ListAgents, admin, plant, "", http.StatusOK},
		{"viewer reads its vhost", h.GetAgent, viewerA, id, "", http.StatusOK},
		{"viewer may not add", h.CreateAgent, viewerA, plant, `{"name":"x"}`, http.StatusForbidden},
		{"viewer may not change", h.UpdateAgent, viewerA, id, `{"name":"x"}`, http.StatusForbidden},
		{"viewer may not replace the token", h.ReplaceToken, viewerA, id, "", http.StatusForbidden},
		{"viewer may not delete", h.DeleteAgent, viewerA, id, "", http.StatusForbidden},
		{"other vhost may not list", h.ListAgents, editorB, plant, "", http.StatusForbidden},
		{"other vhost may not read", h.GetAgent, editorB, id, "", http.StatusNotFound},
		{"other vhost may not change", h.UpdateAgent, editorB, id, `{"name":"x"}`, http.StatusNotFound},
		{"other vhost may not take the token", h.ReplaceToken, editorB, id, "", http.StatusNotFound},
		{"other vhost may not delete", h.DeleteAgent, editorB, id, "", http.StatusNotFound},
		{"anonymous", h.ListAgents, nil, plant, "", http.StatusForbidden},
		{"all is not a vhost", h.CreateAgent, admin, map[string]string{"vhost": "all"}, `{"name":"x"}`, http.StatusBadRequest},
		{"no name", h.CreateAgent, editorA, plant, `{"name":""}`, http.StatusBadRequest},
		{"not JSON", h.CreateAgent, editorA, plant, `nope`, http.StatusBadRequest},
		{"missing agent", h.GetAgent, admin, map[string]string{"id": "nope"}, "", http.StatusNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if w := call(c.fn, c.user, http.MethodGet, c.path, c.body); w.Code != c.want {
				t.Fatalf("= %d %s, want %d", w.Code, w.Body, c.want)
			}
		})
	}
}
