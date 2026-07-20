package http

import (
	"github.com/user/hermod/internal/api/handlers"
)

type WebhookHandler struct {
	*handlers.Handler
}

func NewWebhookHandler(h *handlers.Handler) *WebhookHandler {
	return &WebhookHandler{Handler: h}
}
