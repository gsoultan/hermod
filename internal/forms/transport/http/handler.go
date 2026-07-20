package http

import (
	"github.com/user/hermod/internal/api/handlers"
)

type FormHandler struct {
	*handlers.Handler
}

func NewFormHandler(h *handlers.Handler) *FormHandler {
	return &FormHandler{Handler: h}
}
