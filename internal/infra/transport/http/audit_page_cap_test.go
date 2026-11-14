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
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
)

type auditPageSpy struct {
	storage.Storage
	asked storage.AuditFilter
}

func (s *auditPageSpy) ListAuditLogs(_ context.Context, f storage.AuditFilter) ([]storage.AuditLog, int, error) {
	s.asked = f
	return nil, 0, nil
}

func TestAuditLogPageSizeIsCapped(t *testing.T) {
	spy := &auditPageSpy{}
	h := NewInfraHandler(&handlers.Handler{LogStorage: spy})
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "GET", "/api/audit-logs?limit=2000000000", nil)
	admin := &storage.User{ID: "u1", Role: storage.RoleAdministrator}
	h.ListAuditLogs(rec, req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey, admin)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if spy.asked.Limit != handlers.MaxListPageSize {
		t.Errorf("asked storage for %d rows, want the cap of %d", spy.asked.Limit, handlers.MaxListPageSize)
	}
}
