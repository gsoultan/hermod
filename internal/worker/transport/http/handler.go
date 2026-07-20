package http

import (
	"github.com/user/hermod/internal/api/handlers"
)

type WorkerHandler struct {
	*handlers.Handler
}

func NewWorkerHandler(h *handlers.Handler) *WorkerHandler {
	return &WorkerHandler{Handler: h}
}
