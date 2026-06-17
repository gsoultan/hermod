package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

const (
	// maxUploadBytes caps the size of an uploaded file (10 MB).
	maxUploadBytes = 10 << 20
)

// allowedUploadExtensions is an allow-list of file extensions accepted by the
// upload endpoint. Anything not listed here is rejected to avoid storing
// executables, scripts, or other dangerous content.
var allowedUploadExtensions = map[string]struct{}{
	".csv":     {},
	".tsv":     {},
	".json":    {},
	".jsonl":   {},
	".ndjson":  {},
	".xml":     {},
	".yaml":    {},
	".yml":     {},
	".txt":     {},
	".parquet": {},
	".avro":    {},
	".xlsx":    {},
	".png":     {},
	".jpg":     {},
	".jpeg":    {},
	".svg":     {},
	".gif":     {},
	".pem":     {},
	".crt":     {},
	".key":     {},
	".sql":     {},
	".gz":      {},
	".zip":     {},
}

// uploadFile handles multipart file uploads, sanitizes the filename, and
// stores the file using the configured file storage, returning the path or URI.
func (h *Handler) UploadFile(w http.ResponseWriter, r *http.Request) {
	if h.FileStorage == nil {
		http.Error(w, "File storage not initialized", http.StatusInternalServerError)
		return
	}

	// Enforce the size limit at the body level so oversized uploads are rejected
	// before being buffered to memory/disk.
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		http.Error(w, "Failed to parse form (file too large or malformed)", http.StatusBadRequest)
		return
	}

	file, handler, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Failed to get file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Sanitize filename and prevent path traversal/overwrites.
	name := filepath.Base(handler.Filename)
	name = strings.ReplaceAll(name, "..", "_")

	// Enforce the extension allow-list.
	ext := strings.ToLower(filepath.Ext(name))
	if _, ok := allowedUploadExtensions[ext]; !ok {
		http.Error(w, "Unsupported file type", http.StatusUnsupportedMediaType)
		return
	}

	if name == "" || name == ext {
		name = fmt.Sprintf("upload-%d%s", time.Now().UnixNano(), ext)
	} else {
		// Add timestamp to ensure uniqueness.
		base := strings.TrimSuffix(name, filepath.Ext(name))
		name = fmt.Sprintf("%s-%d%s", base, time.Now().UnixNano(), ext)
	}

	path, err := h.FileStorage.Save(r.Context(), name, file)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to save file: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"path": path,
	})
}
