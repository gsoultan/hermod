package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// uploadFile handles multipart file uploads, sanitizes the filename, and
// stores the file under the local uploads directory, returning the absolute path.
func (s *Server) uploadFile(w http.ResponseWriter, r *http.Request) {
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

	// Ensure uploads directory exists
	uploadDir := "uploads"
	if _, err := os.Stat(uploadDir); os.IsNotExist(err) {
		os.Mkdir(uploadDir, 0755)
	}

	// Sanitize filename and prevent path traversal/overwrites
	name := filepath.Base(handler.Filename)
	name = strings.ReplaceAll(name, "..", "_")
	if name == "" {
		name = fmt.Sprintf("upload-%d.bin", time.Now().UnixNano())
	}
	filePath := filepath.Join(uploadDir, name)
	// If file exists, add a timestamp suffix
	if _, err := os.Stat(filePath); err == nil {
		ext := filepath.Ext(name)
		base := strings.TrimSuffix(name, ext)
		filePath = filepath.Join(uploadDir, fmt.Sprintf("%s-%d%s", base, time.Now().UnixNano(), ext))
	}
	dst, err := os.Create(filePath)
	if err != nil {
		http.Error(w, "Failed to create file", http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	if _, err := io.Copy(dst, file); err != nil {
		http.Error(w, "Failed to save file", http.StatusInternalServerError)
		return
	}

	// Get absolute path
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		absPath = filePath
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"path": absPath,
	})
}
