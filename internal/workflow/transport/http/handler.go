package http

import (
	"github.com/user/hermod/internal/api/handlers"
)

type WorkflowHandler struct {
	*handlers.Handler
}

func NewWorkflowHandler(h *handlers.Handler) *WorkflowHandler {
	return &WorkflowHandler{Handler: h}
}
