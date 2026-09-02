package http

import (
	"github.com/gsoultan/Hermod/internal/api/handlers"
)

type ApprovalHandler struct {
	*handlers.Handler
}

func NewApprovalHandler(h *handlers.Handler) *ApprovalHandler {
	return &ApprovalHandler{Handler: h}
}
