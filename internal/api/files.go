package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

// uploadFile handles multipart file uploads, sanitizes the filename, and
// stores the file using the configured file storage, returning the path or URI.
func (s *Server) uploadFile(w http.ResponseWriter, r *http.Request) {
	if s.fileStorage == nil {
		http.Error(w, "File storage not initialized", http.StatusInternalServerError)
		return
	}

	err := r.ParseMultipartForm(10 << 20) // 10 MB
	if err != nil {
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	file, handler, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Failed to get file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Sanitize filename and prevent path traversal/overwrites
	name := filepath.Base(handler.Filename)
	name = strings.ReplaceAll(name, "..", "_")
	if name == "" {
		name = fmt.Sprintf("upload-%d.bin", time.Now().UnixNano())
	} else {
		// Add timestamp to ensure uniqueness
		ext := filepath.Ext(name)
		base := strings.TrimSuffix(name, ext)
		name = fmt.Sprintf("%s-%d%s", base, time.Now().UnixNano(), ext)
	}

	path, err := s.fileStorage.Save(r.Context(), name, file)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to save file: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"path": path,
	})
}
