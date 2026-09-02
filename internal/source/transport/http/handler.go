package http

import (
	"github.com/gsoultan/Hermod/internal/api/handlers"
)

type SourceHandler struct {
	*handlers.Handler
}

func NewSourceHandler(h *handlers.Handler) *SourceHandler {
	return &SourceHandler{Handler: h}
}
