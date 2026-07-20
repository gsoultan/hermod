package http

import (
	"github.com/user/hermod/internal/api/handlers"
)

type SinkHandler struct {
	*handlers.Handler
}

func NewSinkHandler(h *handlers.Handler) *SinkHandler {
	return &SinkHandler{Handler: h}
}
