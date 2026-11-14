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
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/internal/testutil"
)

// duplicateNameStore refuses a second user or vhost of a name the way a
// database does: with its own constraint error, which is not a message for a
// person.
type duplicateNameStore struct {
	testutil.BaseMockStorage
	users  []storage.User
	vhosts []storage.VHost
}

var errUnique = errors.New(`UNIQUE constraint failed: users.username (2067)`)

func (s *duplicateNameStore) ListUsers(context.Context, storage.CommonFilter) ([]storage.User, int, error) {
	return s.users, len(s.users), nil
}

func (s *duplicateNameStore) GetUser(_ context.Context, id string) (storage.User, error) {
	for _, u := range s.users {
		if u.ID == id {
			return u, nil
		}
	}
	return storage.User{}, storage.ErrNotFound
}

func (s *duplicateNameStore) GetUserByUsername(_ context.Context, name string) (storage.User, error) {
	for _, u := range s.users {
		if u.Username == name {
			return u, nil
		}
	}
	return storage.User{}, storage.ErrNotFound
}

func (s *duplicateNameStore) CreateUser(_ context.Context, user storage.User) error {
	for _, u := range s.users {
		if u.Username == user.Username {
			return errUnique
		}
	}
	s.users = append(s.users, user)
	return nil
}

func (s *duplicateNameStore) UpdateUser(_ context.Context, user storage.User) error {
	for _, u := range s.users {
		if u.ID != user.ID && u.Username == user.Username {
			return errUnique
		}
	}
	return nil
}

func (s *duplicateNameStore) ListVHosts(context.Context, storage.CommonFilter) ([]storage.VHost, int, error) {
	return s.vhosts, len(s.vhosts), nil
}

func (s *duplicateNameStore) CreateVHost(_ context.Context, vhost storage.VHost) error {
	for _, v := range s.vhosts {
		if v.Name == vhost.Name {
			return errUnique
		}
	}
	s.vhosts = append(s.vhosts, vhost)
	return nil
}

func duplicateNameCall(t *testing.T, serve http.HandlerFunc, method, id, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), method, "/", strings.NewReader(body))
	if id != "" {
		r.SetPathValue("id", id)
	}
	admin := &storage.User{ID: "root", Username: "root", Role: storage.RoleAdministrator}
	r = r.WithContext(context.WithValue(r.Context(), handlers.UserContextKey, admin))
	w := httptest.NewRecorder()
	serve(w, r)
	return w
}

// A second user or vhost under a name already taken answered 500 with the
// database's own constraint error in the message. The name is the caller's
// mistake and easily put right, so it is a 409 that says which name is taken.
func TestATakenNameIsAConflictNotAServerError(t *testing.T) {
	newHandler := func() *AuthHandler {
		store := &duplicateNameStore{
			users: []storage.User{
				{ID: "u1", Username: "ana", Role: storage.RoleEditor},
				{ID: "u2", Username: "budi", Role: storage.RoleViewer},
			},
			vhosts: []storage.VHost{{ID: "v1", Name: "sales"}},
		}
		return &AuthHandler{Handler: &handlers.Handler{Storage: store, LogStorage: store}}
	}

	for _, tc := range []struct {
		name   string
		call   func(h *AuthHandler) *httptest.ResponseRecorder
		status int
		says   string
	}{
		{"a second user named ana", func(h *AuthHandler) *httptest.ResponseRecorder {
			return duplicateNameCall(t, h.CreateUser, http.MethodPost, "", `{"username":"ana","password":"long-enough-pw","role":"Viewer"}`)
		}, http.StatusConflict, "ana"},
		{"a user named cici", func(h *AuthHandler) *httptest.ResponseRecorder {
			return duplicateNameCall(t, h.CreateUser, http.MethodPost, "", `{"username":"cici","password":"long-enough-pw","role":"Viewer"}`)
		}, http.StatusCreated, ""},
		{"budi renamed to ana", func(h *AuthHandler) *httptest.ResponseRecorder {
			return duplicateNameCall(t, h.UpdateUser, http.MethodPut, "u2", `{"username":"ana"}`)
		}, http.StatusConflict, "ana"},
		{"ana saved under her own name", func(h *AuthHandler) *httptest.ResponseRecorder {
			return duplicateNameCall(t, h.UpdateUser, http.MethodPut, "u1", `{"username":"ana"}`)
		}, http.StatusOK, ""},
		{"a second vhost named sales", func(h *AuthHandler) *httptest.ResponseRecorder {
			return duplicateNameCall(t, h.CreateVHost, http.MethodPost, "", `{"name":"sales"}`)
		}, http.StatusConflict, "sales"},
		{"a vhost named ops", func(h *AuthHandler) *httptest.ResponseRecorder {
			return duplicateNameCall(t, h.CreateVHost, http.MethodPost, "", `{"name":"ops"}`)
		}, http.StatusCreated, ""},
	} {
		rec := tc.call(newHandler())
		if rec.Code != tc.status {
			t.Errorf("%s: status %d, want %d: %s", tc.name, rec.Code, tc.status, rec.Body)
			continue
		}
		if strings.Contains(rec.Body.String(), "UNIQUE") {
			t.Errorf("%s: the database's constraint error reached the caller: %s", tc.name, rec.Body)
		}
		if tc.says != "" && !strings.Contains(rec.Body.String(), tc.says) {
			t.Errorf("%s: the answer should name %q: %s", tc.name, tc.says, rec.Body)
		}
	}
}
