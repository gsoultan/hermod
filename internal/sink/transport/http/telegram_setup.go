// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gsoultan/hermod"
	sinktelegram "github.com/gsoultan/hermod/pkg/comm/sink/telegram"
)

// telegramAPIBase is the Bot API root the setup helpers call. Only tests
// write it.
var telegramAPIBase = sinktelegram.DefaultBaseURL

// telegramSetupTimeout bounds one setup call: getMe and getUpdates, or one
// sendMessage.
const telegramSetupTimeout = 15 * time.Second

// maxTelegramSetupBytes bounds a request: a token, a chat ID and a topic.
const maxTelegramSetupBytes = 64 << 10

// telegramTestText is what a test send posts.
const telegramTestText = "✅ Hermod can post here. This is a test message from the Telegram sink setup."

// telegramSetupRequest is what the form sends: the sink's vhost, whose
// secrets the token may reference, and its config as it stands.
type telegramSetupRequest struct {
	VHost  string           `json:"vhost"`
	Config hermod.StringMap `json:"config"`
}

// telegramSetupError is a refusal with Telegram's fix for it.
type telegramSetupError struct {
	Error           string `json:"error"`
	Hint            string `json:"hint,omitempty"`
	MigrateToChatID string `json:"migrate_to_chat_id,omitempty"`
}

// FindTelegramChats answers with the bot a token belongs to and the chats it
// can post to, so the form can offer a person, group or channel to pick
// instead of asking for a chat ID nobody knows how to find.
func (h *SinkHandler) FindTelegramChats(w http.ResponseWriter, r *http.Request) {
	cfg, ctx, cancel, ok := h.telegramSetupConfig(w, r)
	if !ok {
		return
	}
	defer cancel()
	chats, err := sinktelegram.FindChats(ctx, telegramAPIBase, telegramToken(cfg))
	if err != nil {
		writeTelegramSetupError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(chats)
}

// SendTelegramTestMessage posts a test message to the configured chat and
// topic, with the token a run would use.
//
// Test connection calls getMe, which proves the token and nothing about the
// chat; this proves the bot can post where the sink will.
func (h *SinkHandler) SendTelegramTestMessage(w http.ResponseWriter, r *http.Request) {
	cfg, ctx, cancel, ok := h.telegramSetupConfig(w, r)
	if !ok {
		return
	}
	defer cancel()
	sent, err := sinktelegram.SendTestMessage(ctx, telegramAPIBase, telegramToken(cfg), cfg["chat_id"], cfg["message_thread_id"], telegramTestText)
	if err != nil {
		writeTelegramSetupError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(sent)
}

// telegramSetupConfig decodes the request, checks the caller may use its
// vhost and resolves its secrets the way a run building the sink does.
func (h *SinkHandler) telegramSetupConfig(w http.ResponseWriter, r *http.Request) (map[string]string, context.Context, context.CancelFunc, bool) {
	var req telegramSetupRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxTelegramSetupBytes)).Decode(&req); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return nil, nil, nil, false
	}
	// Naming a vhost is asking for its secrets.
	if !h.AllowConnectorVHost(w, r, req.VHost) {
		return nil, nil, nil, false
	}
	ctx, cancel := context.WithTimeout(r.Context(), telegramSetupTimeout)
	resolved, err := h.Registry.ResolveSecrets(ctx, req.VHost, req.Config)
	if err != nil {
		cancel()
		writeTelegramSetupError(w, err)
		return nil, nil, nil, false
	}
	return resolved, ctx, cancel, true
}

// telegramToken reads the token the way the factory does: the form writes
// bot_token, older sinks hold token.
func telegramToken(cfg map[string]string) string {
	if v := cfg["bot_token"]; v != "" {
		return v
	}
	return cfg["token"]
}

func writeTelegramSetupError(w http.ResponseWriter, err error) {
	body := telegramSetupError{Error: err.Error()}
	var apiErr *sinktelegram.APIError
	if errors.As(err, &apiErr) {
		body = telegramSetupError{Error: apiErr.Description, Hint: apiErr.Hint, MigrateToChatID: apiErr.MigrateToChatID}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(body)
}
