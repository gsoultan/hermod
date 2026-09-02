package http

import (
	"github.com/gsoultan/Hermod/internal/api/handlers"
)

type SSEHandler struct {
	*handlers.Handler
}

func NewSSEHandler(h *handlers.Handler) *SSEHandler {
	return &SSEHandler{Handler: h}
}
