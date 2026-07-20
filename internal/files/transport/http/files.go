package http

import (
	"net/http"

	"github.com/user/hermod/internal/api/handlers"
)

type FileHandler struct {
	*handlers.Handler
}

func NewFileHandler(h *handlers.Handler) *FileHandler {
	return &FileHandler{Handler: h}
}

func (h *FileHandler) RegisterFileRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/files/upload", h.UploadFile)
}
