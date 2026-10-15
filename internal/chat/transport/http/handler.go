// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

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
