package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/user/hermod/internal/config"
	"github.com/user/hermod/internal/storage"
)

func withAdminContext(r *http.Request) *http.Request {
	admin := &storage.User{Role: storage.RoleAdministrator}
	ctx := context.WithValue(r.Context(), userContextKey, admin)
	return r.WithContext(ctx)
}

func TestGetDBConfig_NotConfigured_Returns404(t *testing.T) {
	// Use temp working dir to avoid touching real repo files
	tempDir := t.TempDir()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/api/config/database", nil)
	req = withAdminContext(req)
	rr := httptest.NewRecorder()

	s.getDBConfig(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
	var body map[string]string
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if body["error"] == "" {
		t.Fatalf("expected error message in response")
	}
}

func TestGetDBConfig_Configured_Returns200Masked(t *testing.T) {
	tempDir := t.TempDir()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	cfg := &config.DBConfig{
		Type:            "postgres",
		Conn:            "postgres://user:pass@localhost:5432/db",
		JWTSecret:       "secret",
		CryptoMasterKey: "1234567890123456",
	}
	if err := config.SaveDBConfig(cfg); err != nil {
		t.Fatalf("SaveDBConfig: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tempDir, config.DBConfigPath)); err != nil {
		t.Fatalf("db_config.yaml not created: %v", err)
	}

	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/api/config/database", nil)
	req = withAdminContext(req)
	rr := httptest.NewRecorder()

	s.getDBConfig(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["type"] != "postgres" {
		t.Fatalf("type = %q, want postgres", body["type"])
	}
	masked := body["conn"]
	// Accept either literal asterisks or URL-encoded asterisks in the password field
	hasAsterisks := strings.Contains(masked, "****") || strings.Contains(masked, "%2A%2A%2A%2A")
	if !hasAsterisks || strings.Contains(masked, "pass") {
		t.Fatalf("conn not masked properly: %q", masked)
	}
}

func TestGetDBConfig_Forbidden_ForNonAdmin(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/api/config/database", nil)
	rr := httptest.NewRecorder()

	s.getDBConfig(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
}

func TestGetDBConfig_CorruptYAML_Returns500(t *testing.T) {
	tempDir := t.TempDir()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	// Write an invalid YAML file
	if err := os.WriteFile(config.DBConfigPath, []byte(": : :"), 0644); err != nil {
		t.Fatalf("write corrupt yaml: %v", err)
	}

	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/api/config/database", nil)
	req = withAdminContext(req)
	rr := httptest.NewRecorder()

	s.getDBConfig(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rr.Code)
	}
}
