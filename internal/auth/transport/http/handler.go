package http

import (
	"github.com/user/hermod/internal/api/handlers"
)

type AuthHandler struct {
	*handlers.Handler
}

func NewAuthHandler(h *handlers.Handler) *AuthHandler {
	return &AuthHandler{Handler: h}
}
