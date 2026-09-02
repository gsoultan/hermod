package http

import (
	"github.com/gsoultan/hermod/internal/api/handlers"
)

type DashboardHandler struct {
	*handlers.Handler
}

func NewDashboardHandler(h *handlers.Handler) *DashboardHandler {
	return &DashboardHandler{Handler: h}
}
