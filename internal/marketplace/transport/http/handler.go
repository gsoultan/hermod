// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package http

import (
	"github.com/gsoultan/hermod/internal/api/handlers"
)

type MarketplaceHandler struct {
	*handlers.Handler
}

func NewMarketplaceHandler(h *handlers.Handler) *MarketplaceHandler {
	return &MarketplaceHandler{Handler: h}
}
