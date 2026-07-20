package http

import (
	"github.com/user/hermod/internal/api/handlers"
)

type LogHandler struct {
	*handlers.Handler
}

func NewLogHandler(h *handlers.Handler) *LogHandler {
	return &LogHandler{Handler: h}
}
