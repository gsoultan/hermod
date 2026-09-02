package http

import (
	"github.com/gsoultan/hermod/internal/api/handlers"
)

type ApprovalHandler struct {
	*handlers.Handler
}

func NewApprovalHandler(h *handlers.Handler) *ApprovalHandler {
	return &ApprovalHandler{Handler: h}
}
