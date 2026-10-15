// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

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
