package http

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/engine/registry"
	"github.com/gsoultan/hermod/internal/storage"
)

// A fresh install starts with no database, so main.go builds the registry with
// nil storage and hands it to the API. FinalizeInitialSetup then opens the
// database the admin chose and swaps it into the handler — but it used to stop
// there, leaving the registry still holding nil.
//
// The result was an install that looked complete and could not run anything:
// every attempt to start a workflow failed with "registry storage is not
// initialized", and only restarting the process fixed it. Nothing in the UI
// said so, and the E2E suite missed it because activation is asserted on the
// status badge rather than on the engine.
func TestFinalizeInitialSetupGivesTheRegistryItsStorage(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HERMOD_CONFIG_DIR", dir)

	// The registry as main.go builds it on a first run: no storage.
	reg := registry.NewRegistry(nil)
	h := NewInfraHandler(&handlers.Handler{
		Registry:   reg,
		ConfigPath: filepath.Join(dir, "config.yaml"),
	})

	body, err := json.Marshal(map[string]any{
		"db": map[string]string{
			"type":              "sqlite",
			"conn":              filepath.Join(dir, "hermod.db"),
			"crypto_master_key": "0123456789abcdef0123456789abcdef",
		},
		"admin": map[string]string{
			"username": "admin",
			"password": "admin-password",
			"email":    "admin@example.com",
		},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/config/setup", bytes.NewReader(body))
	h.FinalizeInitialSetup(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("setup returned %d, body: %s", rec.Code, rec.Body.String())
	}

	// Ask the registry to start a workflow. The workflow does not exist, so an
	// error is expected — but it must be about the workflow, not about the
	// registry having no storage to look it up in.
	startErr := reg.StartWorkflow("does-not-exist", storage.Workflow{ID: "does-not-exist"})
	if startErr != nil && strings.Contains(startErr.Error(), "registry storage is not initialized") {
		t.Fatalf("registry still has no storage after setup: %v", startErr)
	}
}

// The worker is what actually runs workflows once the registry can find them.
// On a first run main.go does not start one (shouldStartWorker requires the
// install to be complete at process start), so the handler holds a nil Worker.
// Setup must hand the new storage to a worker if one is present, and must not
// panic when one is not.
func TestFinalizeInitialSetupUpdatesTheWorkerWhenPresent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HERMOD_CONFIG_DIR", dir)

	spy := &workerStorageSpy{}
	h := NewInfraHandler(&handlers.Handler{
		Registry:   registry.NewRegistry(nil),
		Worker:     spy,
		ConfigPath: filepath.Join(dir, "config.yaml"),
	})

	body, _ := json.Marshal(map[string]any{
		"db": map[string]string{
			"type":              "sqlite",
			"conn":              filepath.Join(dir, "hermod.db"),
			"crypto_master_key": "0123456789abcdef0123456789abcdef",
		},
		"admin": map[string]string{"username": "admin", "password": "admin-password"},
	})

	rec := httptest.NewRecorder()
	h.FinalizeInitialSetup(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/config/setup", bytes.NewReader(body)))

	if rec.Code != http.StatusOK {
		t.Fatalf("setup returned %d, body: %s", rec.Code, rec.Body.String())
	}
	if !spy.gotStorage {
		t.Fatal("worker was never given the storage opened during setup")
	}
}

type workerStorageSpy struct {
	gotStorage bool
}

func (w *workerStorageSpy) SetStorage(s storage.Storage) {
	if s != nil {
		w.gotStorage = true
	}
}

func (w *workerStorageSpy) RequestShutdown(string) {}

// A first run starts with no worker at all: there was no database when the
// process booted, so main.go had nothing to build one from. Setup is the moment
// that changes, and it has to say so — otherwise the install runs without the
// component that picks up workflows assigned to a worker until someone
// restarts it, which is the last piece of the first-run gap.
//
// main supplies the callback because starting a worker needs the process
// context and cancel func, which live there and have no business in a handler.
func TestFinalizeInitialSetupAnnouncesTheNewStorage(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HERMOD_CONFIG_DIR", dir)

	var got storage.Storage
	calls := 0
	h := NewInfraHandler(&handlers.Handler{
		Registry:   registry.NewRegistry(nil),
		ConfigPath: filepath.Join(dir, "config.yaml"),
		OnSetupComplete: func(s storage.Storage) {
			calls++
			got = s
		},
	})

	body, _ := json.Marshal(map[string]any{
		"db": map[string]string{
			"type":              "sqlite",
			"conn":              filepath.Join(dir, "hermod.db"),
			"crypto_master_key": "0123456789abcdef0123456789abcdef",
		},
		"admin": map[string]string{"username": "admin", "password": "admin-password"},
	})

	rec := httptest.NewRecorder()
	h.FinalizeInitialSetup(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/config/setup", bytes.NewReader(body)))

	if rec.Code != http.StatusOK {
		t.Fatalf("setup returned %d, body: %s", rec.Code, rec.Body.String())
	}
	if calls != 1 {
		t.Fatalf("OnSetupComplete called %d times, want exactly 1", calls)
	}
	if got == nil {
		t.Fatal("OnSetupComplete was called without the storage setup had just opened")
	}
}

// Setup must not fire the callback when it fails — a half-configured install
// starting a worker is worse than one that starts nothing.
func TestFinalizeInitialSetupDoesNotAnnounceOnFailure(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HERMOD_CONFIG_DIR", dir)

	calls := 0
	h := NewInfraHandler(&handlers.Handler{
		Registry:        registry.NewRegistry(nil),
		ConfigPath:      filepath.Join(dir, "config.yaml"),
		OnSetupComplete: func(storage.Storage) { calls++ },
	})

	// Too short a master key: rejected before any storage is opened.
	body, _ := json.Marshal(map[string]any{
		"db":    map[string]string{"type": "sqlite", "conn": filepath.Join(dir, "h.db"), "crypto_master_key": "short"},
		"admin": map[string]string{"username": "admin", "password": "admin-password"},
	})

	rec := httptest.NewRecorder()
	h.FinalizeInitialSetup(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/config/setup", bytes.NewReader(body)))

	if rec.Code == http.StatusOK {
		t.Fatalf("expected setup to be rejected, got %d", rec.Code)
	}
	if calls != 0 {
		t.Fatalf("OnSetupComplete fired %d times on a failed setup, want 0", calls)
	}
}
