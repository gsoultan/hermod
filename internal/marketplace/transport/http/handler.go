package http

import (
	"github.com/user/hermod/internal/api/handlers"
)

type MarketplaceHandler struct {
	*handlers.Handler
}

func NewMarketplaceHandler(h *handlers.Handler) *MarketplaceHandler {
	return &MarketplaceHandler{Handler: h}
}
