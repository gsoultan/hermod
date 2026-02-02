package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/user/hermod/internal/config"
	"github.com/user/hermod/internal/engine"
	"github.com/user/hermod/internal/mesh"
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

func TestMeshHealth_Combined(t *testing.T) {
	ms := &mockStorageMesh{}
	reg := engine.NewRegistry(ms)
	s := NewServer(reg, ms, nil, nil)

	// Register a cluster in mesh manager
	mm := reg.GetMeshManager()
	mm.RegisterCluster(mesh.Cluster{
		ID:       "remote-1",
		Region:   "us-east-1",
		Endpoint: "https://remote-1.hermod.io",
		Status:   "online",
	})

	req := httptest.NewRequest(http.MethodGet, "/api/infra/mesh-health", nil)
	rr := httptest.NewRecorder()

	s.getMeshHealth(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var health []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &health); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Should have 1 worker (from mock) and 1 cluster
	if len(health) != 2 {
		t.Fatalf("len(health) = %d, want 2", len(health))
	}

	foundCluster := false
	foundWorker := false
	for _, h := range health {
		if h["type"] == "cluster" && h["id"] == "remote-1" {
			foundCluster = true
		}
		if h["type"] == "worker" && h["id"] == "worker-1" {
			foundWorker = true
		}
	}

	if !foundCluster {
		t.Error("remote cluster not found in health response")
	}
	if !foundWorker {
		t.Error("local worker not found in health response")
	}
}

func TestRegisterMeshCluster(t *testing.T) {
	ms := &mockStorageMesh{}
	reg := engine.NewRegistry(ms)
	s := NewServer(reg, ms, nil, nil)

	cluster := mesh.Cluster{
		ID:       "new-cluster",
		Region:   "eu-west-1",
		Endpoint: "https://new.hermod.io",
	}
	body, _ := json.Marshal(cluster)

	req := httptest.NewRequest(http.MethodPost, "/api/mesh/clusters", bytes.NewReader(body))
	req = withAdminContext(req)
	rr := httptest.NewRecorder()

	s.registerMeshCluster(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rr.Code)
	}

	// Verify it's in the manager
	mm := reg.GetMeshManager()
	c, ok := mm.GetCluster("new-cluster")
	if !ok {
		t.Fatal("cluster not registered in manager")
	}
	if c.Region != "eu-west-1" {
		t.Errorf("region = %q, want eu-west-1", c.Region)
	}
}

type mockStorageMesh struct {
	mockStorage
}

func (m *mockStorageMesh) ListWorkers(ctx context.Context, filter storage.CommonFilter) ([]storage.Worker, int, error) {
	now := time.Now()
	return []storage.Worker{
		{ID: "worker-1", Name: "Worker 1", LastSeen: &now, CPUUsage: 0.5, MemoryUsage: 512},
	}, 1, nil
}

func (m *mockStorageMesh) ListWorkflows(ctx context.Context, filter storage.CommonFilter) ([]storage.Workflow, int, error) {
	return nil, 0, nil
}
