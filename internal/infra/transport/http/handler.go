package http

import (
	"github.com/gsoultan/Hermod/internal/api/handlers"
)

type InfraHandler struct {
	*handlers.Handler
}

func NewInfraHandler(h *handlers.Handler) *InfraHandler {
	return &InfraHandler{Handler: h}
}
