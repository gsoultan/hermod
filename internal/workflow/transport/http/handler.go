package http

import (
	"github.com/gsoultan/hermod/internal/api/handlers"
)

type WorkflowHandler struct {
	*handlers.Handler
}

func NewWorkflowHandler(h *handlers.Handler) *WorkflowHandler {
	return &WorkflowHandler{Handler: h}
}
