package handlers

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

func (h *Handler) RegisterMarketplaceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/marketplace/plugins", h.HandleListPlugins)
	mux.HandleFunc("GET /api/marketplace/plugins/{id}", h.HandleGetPlugin)
	mux.Handle("POST /api/marketplace/install", h.EditorOnly(h.HandleInstallPlugin))
	mux.Handle("POST /api/marketplace/uninstall", h.EditorOnly(h.HandleUninstallPlugin))
}

func (h *Handler) GetPluginCacheDir() string {
	dir := filepath.Join("data", "plugins")
	_ = os.MkdirAll(dir, 0755)
	return dir
}

func (h *Handler) HandleListPlugins(w http.ResponseWriter, r *http.Request) {
	plugins, err := h.Storage.ListPlugins(r.Context())
	if err != nil {
		h.JsonError(w, "Failed to list plugins: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(plugins)
}

func (h *Handler) HandleInstallPlugin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.JsonError(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	if req.ID == "" {
		h.JsonError(w, "Plugin ID is required", http.StatusBadRequest)
		return
	}

	plugin, err := h.Storage.GetPlugin(r.Context(), req.ID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			h.JsonError(w, "Plugin not found", http.StatusNotFound)
			return
		}
		h.JsonError(w, "Failed to get plugin metadata: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Download WASM if URL is provided
	if plugin.WasmURL != "" {
		resp, err := http.Get(plugin.WasmURL)
		if err != nil {
			h.JsonError(w, fmt.Sprintf("Failed to download plugin WASM from %s: %s", plugin.WasmURL, err.Error()), http.StatusInternalServerError)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			h.JsonError(w, "Failed to download plugin WASM: received status "+resp.Status, http.StatusInternalServerError)
			return
		}

		cacheDir := h.GetPluginCacheDir()
		cachePath := filepath.Join(cacheDir, req.ID+".wasm")
		out, err := os.Create(cachePath)
		if err != nil {
			h.JsonError(w, "Failed to create local plugin file: "+err.Error(), http.StatusInternalServerError)
			return
		}
		defer out.Close()

		_, err = io.Copy(out, resp.Body)
		if err != nil {
			h.JsonError(w, "Failed to save plugin WASM: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	err = h.Storage.InstallPlugin(r.Context(), req.ID)
	if err != nil {
		h.JsonError(w, "Failed to mark plugin as installed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "installed"})
}

func (h *Handler) HandleUninstallPlugin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.JsonError(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	if req.ID == "" {
		h.JsonError(w, "Plugin ID is required", http.StatusBadRequest)
		return
	}

	err := h.Storage.UninstallPlugin(r.Context(), req.ID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			h.JsonError(w, "Plugin not found", http.StatusNotFound)
			return
		}
		h.JsonError(w, "Failed to uninstall plugin: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Remove cached WASM file if exists
	cachePath := filepath.Join(h.GetPluginCacheDir(), req.ID+".wasm")
	_ = os.Remove(cachePath)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "uninstalled"})
}

func (h *Handler) HandleGetPlugin(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		h.JsonError(w, "Plugin ID is required", http.StatusBadRequest)
		return
	}

	plugin, err := h.Storage.GetPlugin(r.Context(), id)
	if err != nil {
		h.JsonError(w, "Plugin not found: "+err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(plugin)
}
