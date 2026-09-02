package http

import (
	"github.com/gsoultan/Hermod/internal/api/handlers"
)

type WSHandler struct {
	*handlers.Handler
}

func NewWSHandler(h *handlers.Handler) *WSHandler {
	return &WSHandler{Handler: h}
}
