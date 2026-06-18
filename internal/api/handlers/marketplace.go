package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/user/hermod/internal/storage"
)

// pluginIDPattern restricts plugin identifiers to a safe character set so they
// can never be used to traverse outside the plugin cache directory.
var pluginIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)

// maxPluginWasmSize caps the size of a downloaded plugin (32 MiB) to prevent
// disk-fill denial-of-service attacks.
const maxPluginWasmSize = 32 << 20

// validatePluginID ensures the identifier is safe to use as a filename and
// cannot escape the plugin cache directory via path traversal.
func validatePluginID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	if strings.Contains(id, "..") {
		return false
	}
	return pluginIDPattern.MatchString(id)
}

// isSafeWasmURL only permits http(s) URLs that do not target loopback,
// link-local, or otherwise private/internal addresses (SSRF protection).
func isSafeWasmURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	host := u.Hostname()
	if host == "" {
		return false
	}
	// Reject direct IP literals that point to private/loopback ranges.
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return false
		}
	}
	return true
}

func (h *Handler) RegisterMarketplaceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/marketplace/plugins", h.HandleListPlugins)
	mux.HandleFunc("GET /api/marketplace/plugins/{id}", h.HandleGetPlugin)
	mux.Handle("POST /api/marketplace/install", h.EditorOnly(h.HandleInstallPlugin))
	mux.Handle("POST /api/marketplace/uninstall", h.EditorOnly(h.HandleUninstallPlugin))
}

func (h *Handler) GetPluginCacheDir() string {
	dir := filepath.Join("data", "plugins")
	_ = os.MkdirAll(dir, 0o750)
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

	if !validatePluginID(req.ID) {
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
func (h *Handler) downloadPluginWasm(ctx context.Context, wasmURL, id string) error {
	if !isSafeWasmURL(wasmURL) {
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

	if _, err := io.Copy(out, io.LimitReader(resp.Body, maxPluginWasmSize)); err != nil {
		return errors.New("failed to save plugin WASM")
	}
	return nil
}

func (h *Handler) HandleUninstallPlugin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.JsonError(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	if !validatePluginID(req.ID) {
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
