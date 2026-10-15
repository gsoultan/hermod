// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
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
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gsoultan/hermod/internal/api/handlers"
)

// The workflow builder is mounted on the server the process runs, behind
// authentication and the Editor guard.
func TestTheServerMountsTheWorkflowBuilderForEditorsOnly(t *testing.T) {
	const secret = "builder-route-secret"
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "db_config.yaml"),
		[]byte("type: sqlite\nconn: \"file::memory:\"\njwt_secret: "+secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERMOD_CONFIG_DIR", dir)
	t.Setenv("HERMOD_JWT_SECRET", secret)

	s := &Server{Handler: &handlers.Handler{Storage: &mockStorage{}}}
	srv := httptest.NewServer(s.Routes())
	defer srv.Close()

	post := func(role string) int {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+"/api/ai/build-workflow",
			strings.NewReader(`{"description":"x","connection":{}}`))
		if err != nil {
			t.Fatal(err)
		}
		if role != "" {
			token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
				"id": "u1", "username": "alice", "role": role, "exp": time.Now().Add(time.Hour).Unix(),
			}).SignedString([]byte(secret))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	if code := post(""); code != http.StatusUnauthorized {
		t.Errorf("anonymous: %d, want 401", code)
	}
	if code := post("Viewer"); code != http.StatusForbidden {
		t.Errorf("Viewer: %d, want 403", code)
	}
	// An Editor reaches the handler, which refuses the empty connection.
	if code := post("Editor"); code != http.StatusBadRequest {
		t.Errorf("Editor: %d, want 400 from the handler", code)
	}
}
