package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/user/hermod/internal/storage"
)

func (s *Server) registerMarketplaceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/marketplace/plugins", s.handleListPlugins)
	mux.HandleFunc("GET /api/marketplace/plugins/{id}", s.handleGetPlugin)
	mux.Handle("POST /api/marketplace/install", s.editorOnly(s.handleInstallPlugin))
	mux.Handle("POST /api/marketplace/uninstall", s.editorOnly(s.handleUninstallPlugin))
}

func (s *Server) getPluginCacheDir() string {
	dir := filepath.Join("data", "plugins")
	_ = os.MkdirAll(dir, 0755)
	return dir
}

func (s *Server) handleListPlugins(w http.ResponseWriter, r *http.Request) {
	plugins, err := s.storage.ListPlugins(r.Context())
	if err != nil {
		s.jsonError(w, "Failed to list plugins: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(plugins)
}

func (s *Server) handleInstallPlugin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonError(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	if req.ID == "" {
		s.jsonError(w, "Plugin ID is required", http.StatusBadRequest)
		return
	}

	plugin, err := s.storage.GetPlugin(r.Context(), req.ID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			s.jsonError(w, "Plugin not found", http.StatusNotFound)
			return
		}
		s.jsonError(w, "Failed to get plugin metadata: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Download WASM if URL is provided
	if plugin.WasmURL != "" {
		resp, err := http.Get(plugin.WasmURL)
		if err != nil {
			s.jsonError(w, fmt.Sprintf("Failed to download plugin WASM from %s: %s", plugin.WasmURL, err.Error()), http.StatusInternalServerError)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			s.jsonError(w, fmt.Sprintf("Failed to download plugin WASM: received status %s", resp.Status), http.StatusInternalServerError)
			return
		}

		cacheDir := s.getPluginCacheDir()
		cachePath := filepath.Join(cacheDir, req.ID+".wasm")
		out, err := os.Create(cachePath)
		if err != nil {
			s.jsonError(w, "Failed to create local plugin file: "+err.Error(), http.StatusInternalServerError)
			return
		}
		defer out.Close()

		_, err = io.Copy(out, resp.Body)
		if err != nil {
			s.jsonError(w, "Failed to save plugin WASM: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	err = s.storage.InstallPlugin(r.Context(), req.ID)
	if err != nil {
		s.jsonError(w, "Failed to mark plugin as installed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "installed"})
}

func (s *Server) handleUninstallPlugin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonError(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	if req.ID == "" {
		s.jsonError(w, "Plugin ID is required", http.StatusBadRequest)
		return
	}

	err := s.storage.UninstallPlugin(r.Context(), req.ID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			s.jsonError(w, "Plugin not found", http.StatusNotFound)
			return
		}
		s.jsonError(w, "Failed to uninstall plugin: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Remove cached WASM file if exists
	cachePath := filepath.Join(s.getPluginCacheDir(), req.ID+".wasm")
	_ = os.Remove(cachePath)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "uninstalled"})
}

func (s *Server) handleGetPlugin(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		s.jsonError(w, "Plugin ID is required", http.StatusBadRequest)
		return
	}

	plugin, err := s.storage.GetPlugin(r.Context(), id)
	if err != nil {
		s.jsonError(w, "Plugin not found: "+err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(plugin)
}
