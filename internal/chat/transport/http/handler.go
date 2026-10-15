// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

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
