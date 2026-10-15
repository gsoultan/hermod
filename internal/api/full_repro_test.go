// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
)

type mockStorage struct {
	storage.Storage
}

func (m *mockStorage) ListVHosts(ctx context.Context, filter storage.CommonFilter) ([]storage.VHost, int, error) {
	return []storage.VHost{{Name: "default"}}, 1, nil
}

func (m *mockStorage) ListUsers(ctx context.Context, filter storage.CommonFilter) ([]storage.User, int, error) {
	return nil, 1, nil // return > 0 to not be first run
}

func TestFullServerVHostsRouting(t *testing.T) {
	s := &Server{
		Handler: &handlers.Handler{
			Storage: &mockStorage{},
		},
	}

	handler := s.Routes()
	server := httptest.NewServer(handler)
	defer server.Close()

	// Try without auth first - should get 401, not 404
	resp, err := http.Get(server.URL + "/api/vhosts")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Errorf("Expected not 404, got 404")
	}
}
