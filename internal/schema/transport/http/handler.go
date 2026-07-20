package http

import (
	"github.com/user/hermod/internal/api/handlers"
)

type SchemaHandler struct {
	*handlers.Handler
}

func NewSchemaHandler(h *handlers.Handler) *SchemaHandler {
	return &SchemaHandler{Handler: h}
}
