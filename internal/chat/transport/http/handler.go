// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

// Package http is the chat trigger's transport: the endpoint a chat source
// receives messages on, from a web page, Slack or Telegram, and answers with
// the workflow's reply.
package http

import (
	"net/http"

	"github.com/gsoultan/hermod/internal/api/handlers"
)

// ChatHandler serves chat sources.
type ChatHandler struct {
	*handlers.Handler

	// slackAPI overrides Slack's Web API root. Empty is Slack itself.
	slackAPI string
}

func NewChatHandler(h *handlers.Handler) *ChatHandler {
	return &ChatHandler{Handler: h}
}

// RegisterChatRoutes mounts the chat endpoint and the widget script. Both are
// public: the endpoint checks the source's own credentials.
func (h *ChatHandler) RegisterChatRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/chat/{path...}", h.HandleChat)
	mux.HandleFunc("GET /api/chat/widget.js", h.ServeWidget)
}
