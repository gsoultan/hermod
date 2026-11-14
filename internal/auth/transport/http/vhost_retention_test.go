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
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
)

// A vhost's retention over the API. Setting it decides what is deleted from
// the audit trail, so only an Administrator may; an Editor of the vhost can
// read it, and nobody else can.

type retentionAPIStore struct {
	storage.Storage
	settings map[string]string
	audits   []storage.AuditLog
}

func (s *retentionAPIStore) GetSetting(_ context.Context, key string) (string, error) {
	return s.settings[key], nil
}

func (s *retentionAPIStore) SaveSetting(_ context.Context, key, value string) error {
	s.settings[key] = value
	return nil
}

func (s *retentionAPIStore) CreateLog(context.Context, storage.Log) error { return nil }

func (s *retentionAPIStore) CreateAuditLog(_ context.Context, l storage.AuditLog) error {
	s.audits = append(s.audits, l)
	return nil
}

func newRetentionAPI() (*AuthHandler, *retentionAPIStore) {
	store := &retentionAPIStore{settings: map[string]string{}}
	return &AuthHandler{Handler: &handlers.Handler{Storage: store, LogStorage: store}}, store
}

func TestVHostRetentionAPISavesAndReadsBack(t *testing.T) {
	h, store := newRetentionAPI()

	w := secretCall(h.GetVHostRetention, secretEditorA, http.MethodGet, "tenant-a", "", "")
	if w.Code != http.StatusOK {
		t.Fatalf("reading an unset retention: %d %s", w.Code, w.Body)
	}
	var got storage.VHostRetention
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || !got.IsZero() {
		t.Fatalf("an unset retention reads %s (err %v), want every field 0: keep everything", w.Body, err)
	}

	body := `{"audit_log_days":365,"workflow_versions":50,"approval_days":180}`
	if w := secretCall(h.PutVHostRetention, secretAdmin, http.MethodPut, "tenant-a", "", body); w.Code != http.StatusOK {
		t.Fatalf("saving: %d %s", w.Code, w.Body)
	}
	w = secretCall(h.GetVHostRetention, secretEditorA, http.MethodGet, "tenant-a", "", "")
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if want := (storage.VHostRetention{AuditLogDays: 365, WorkflowVersions: 50, ApprovalDays: 180}); got != want {
		t.Errorf("read back %+v, want %+v", got, want)
	}

	// Another vhost is not touched by it.
	w = secretCall(h.GetVHostRetention, secretAdmin, http.MethodGet, "tenant-b", "", "")
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || !got.IsZero() {
		t.Errorf("tenant-b reads %s after tenant-a was set, want every field 0", w.Body)
	}

	if len(store.audits) != 1 || store.audits[0].VHost != "tenant-a" || !strings.Contains(store.audits[0].Payload, `"audit_log_days":365`) {
		t.Errorf("audit rows = %+v, want one in tenant-a holding the new retention", store.audits)
	}
}

func TestVHostRetentionAPIRefusesWhoMayNotSetIt(t *testing.T) {
	body := `{"audit_log_days":1}`
	for _, tc := range []struct {
		name   string
		call   func(*AuthHandler) http.HandlerFunc
		user   *storage.User
		method string
		want   int
	}{
		{"an editor of the vhost cannot set it", func(h *AuthHandler) http.HandlerFunc { return h.PutVHostRetention }, secretEditorA, http.MethodPut, http.StatusForbidden},
		{"a viewer cannot set it", func(h *AuthHandler) http.HandlerFunc { return h.PutVHostRetention }, secretViewerA, http.MethodPut, http.StatusForbidden},
		{"nobody signed in cannot set it", func(h *AuthHandler) http.HandlerFunc { return h.PutVHostRetention }, nil, http.MethodPut, http.StatusForbidden},
		{"an editor of another vhost cannot read it", func(h *AuthHandler) http.HandlerFunc { return h.GetVHostRetention }, secretEditorB, http.MethodGet, http.StatusForbidden},
		{"a viewer cannot read it", func(h *AuthHandler) http.HandlerFunc { return h.GetVHostRetention }, secretViewerA, http.MethodGet, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, store := newRetentionAPI()
			if w := secretCall(tc.call(h), tc.user, tc.method, "tenant-a", "", body); w.Code != tc.want {
				t.Errorf("status %d, want %d: %s", w.Code, tc.want, w.Body)
			}
			if len(store.settings) != 0 {
				t.Errorf("a refused request saved %v", store.settings)
			}
		})
	}
}

func TestVHostRetentionAPIRefusesABadSetting(t *testing.T) {
	for name, body := range map[string]string{
		"a negative number":      `{"audit_log_days":-1}`,
		"more than ten years":    `{"approval_days":99999}`,
		"too many versions":      `{"workflow_versions":10000000}`,
		"not JSON":               `audit_log_days=3`,
		"a field nobody defined": `{"audit_log_dayz":3}`,
	} {
		t.Run(name, func(t *testing.T) {
			h, store := newRetentionAPI()
			if w := secretCall(h.PutVHostRetention, secretAdmin, http.MethodPut, "tenant-a", "", body); w.Code != http.StatusBadRequest {
				t.Errorf("status %d, want 400: %s", w.Code, w.Body)
			}
			if len(store.settings) != 0 {
				t.Errorf("a refused setting was saved: %v", store.settings)
			}
		})
	}
	h, _ := newRetentionAPI()
	if w := secretCall(h.PutVHostRetention, secretAdmin, http.MethodPut, "all", "", `{}`); w.Code != http.StatusBadRequest {
		t.Errorf("setting the retention of \"all\": %d, want 400", w.Code)
	}
}
