package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/user/hermod/internal/api/handlers"
	"github.com/user/hermod/internal/storage"
)

func (h *MarketplaceHandler) RegisterMarketplaceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/marketplace/plugins", h.HandleListPlugins)
	mux.HandleFunc("GET /api/marketplace/plugins/{id}", h.HandleGetPlugin)
	mux.Handle("POST /api/marketplace/install", h.EditorOnly(h.HandleInstallPlugin))
	mux.Handle("POST /api/marketplace/uninstall", h.EditorOnly(h.HandleUninstallPlugin))
}

func (h *MarketplaceHandler) GetPluginCacheDir() string {
	dir := filepath.Join("data", "plugins")
	_ = os.MkdirAll(dir, 0o750)
	return dir
}

func (h *MarketplaceHandler) HandleListPlugins(w http.ResponseWriter, r *http.Request) {
	plugins, err := h.Storage.ListPlugins(r.Context())
	if err != nil {
		h.JsonError(w, "Failed to list plugins: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(plugins)
}

func (h *MarketplaceHandler) HandleInstallPlugin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.JsonError(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	if !handlers.ValidatePluginID(req.ID) {
		h.JsonError(w, "Invalid plugin ID", http.StatusBadRequest)
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
		if err := h.downloadPluginWasm(r.Context(), plugin.WasmURL, req.ID); err != nil {
			h.JsonError(w, err.Error(), http.StatusBadGateway)
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

// downloadPluginWasm fetches a plugin's WASM payload with SSRF protection,
// a bounded timeout, and a size cap, then stores it in the plugin cache.
func (h *MarketplaceHandler) downloadPluginWasm(ctx context.Context, wasmURL, id string) error {
	if !handlers.IsSafeWasmURL(wasmURL) {
		return errors.New("plugin WASM URL is not allowed")
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, wasmURL, nil)
	if err != nil {
		return fmt.Errorf("failed to build plugin download request: %w", err)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("failed to download plugin WASM")
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return errors.New("failed to download plugin WASM: received status " + resp.Status)
	}

	cachePath := filepath.Join(h.GetPluginCacheDir(), id+".wasm")
	out, err := os.Create(cachePath) //nolint:gosec // id is validated by validatePluginID before use
	if err != nil {
		return errors.New("failed to create local plugin file")
	}
	defer func() { _ = out.Close() }()

	if _, err := io.Copy(out, io.LimitReader(resp.Body, handlers.MaxPluginWasmSize)); err != nil {
		return errors.New("failed to save plugin WASM")
	}
	return nil
}

func (h *MarketplaceHandler) HandleUninstallPlugin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.JsonError(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	if !handlers.ValidatePluginID(req.ID) {
		h.JsonError(w, "Invalid plugin ID", http.StatusBadRequest)
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

func (h *MarketplaceHandler) HandleGetPlugin(w http.ResponseWriter, r *http.Request) {
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
