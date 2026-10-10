package http

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
)

// configTelegramSecret is the secret_token the bot's webhook was set with
// (setWebhook); Telegram sends it as X-Telegram-Bot-Api-Secret-Token.
const configTelegramSecret = "telegram_secret_token"

// telegramMaxText is the longest message Telegram sends, in UTF-16 code units.
const telegramMaxText = 4096

// telegramUpdate is the part of a Bot API update the source reads.
type telegramUpdate struct {
	Message *struct {
		MessageID int64 `json:"message_id"`
		From      *struct {
			ID       int64  `json:"id"`
			IsBot    bool   `json:"is_bot"`
			Username string `json:"username"`
		} `json:"from"`
		Chat struct {
			ID int64 `json:"id"`
		} `json:"chat"`
		Text string `json:"text"`
	} `json:"message"`
}

// handleTelegram serves a chat source set as a Telegram bot's webhook.
//
// The answer goes back in the webhook's own response, as a sendMessage call
// (the Bot API runs a method named in a webhook response), so the source
// needs no bot token and makes no outbound request. Telegram redelivers an
// update that is not answered 2xx, so everything past the secret check is.
func (h *ChatHandler) handleTelegram(w http.ResponseWriter, r *http.Request, src *storage.Source, fullPath string) {
	cfg := src.Config
	up, ok := h.readTelegramUpdate(w, r, cfg)
	if !ok {
		return
	}
	in, chatID, answerable := telegramMessage(up)
	if !answerable {
		count(platformTelegram, "ignored")
		w.WriteHeader(http.StatusOK)
		return
	}
	msg, _ := newChatMessage(fullPath, platformTelegram, in)
	pending, err := h.send(r.Context(), fullPath, msg)
	if err != nil {
		// Not running: Telegram redelivers, by when the workflow may be.
		count(platformTelegram, "not_dispatched")
		h.JsonError(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer pending.Cancel()

	ctx, cancel := context.WithTimeout(r.Context(), timeoutOf(cfg))
	defer cancel()
	outcome, err := pending.Wait(ctx)
	if err != nil {
		count(platformTelegram, "pending")
		h.warn("A Telegram chat message was not answered within the response timeout", "path", fullPath)
		w.WriteHeader(http.StatusOK)
		return
	}
	count(platformTelegram, string(outcome.Status))
	text := ""
	if answered(outcome.Status) {
		text = strings.TrimSpace(replyText(outcome.Record, replyFieldOf(cfg)))
	} else {
		h.warn("A Telegram chat message was not answered: the workflow failed", "path", fullPath, "error", outcome.Error)
	}
	if text == "" {
		w.WriteHeader(http.StatusOK)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"method":  "sendMessage",
		"chat_id": chatID,
		"text":    truncateUTF16(text, telegramMaxText),
	})
}

// readTelegramUpdate checks the update carries the webhook's secret token and
// reads it, answering the caller itself when it cannot.
func (h *ChatHandler) readTelegramUpdate(w http.ResponseWriter, r *http.Request, cfg map[string]string) (telegramUpdate, bool) {
	var up telegramUpdate
	secret := credential(cfg, configTelegramSecret)
	if secret == "" || !handlers.ConstantTimeCompare(r.Header.Get("X-Telegram-Bot-Api-Secret-Token"), secret) {
		count(platformTelegram, "unauthorized")
		h.JsonError(w, "Invalid Telegram secret token", http.StatusUnauthorized)
		return up, false
	}
	body, ok := h.readBody(w, r, maxPlatformBody)
	if !ok {
		return up, false
	}
	if err := json.Unmarshal(body, &up); err != nil {
		h.JsonError(w, "The body is not a Telegram update", http.StatusBadRequest)
		return up, false
	}
	return up, true
}

// telegramMessage is the chat message an update carries, the chat to answer
// in, and whether it is one to answer: a person's text message. A chat is one
// conversation.
func telegramMessage(up telegramUpdate) (in inbound, chatID int64, ok bool) {
	m := up.Message
	if m == nil || m.From == nil || m.From.IsBot || strings.TrimSpace(m.Text) == "" {
		return inbound{}, 0, false
	}
	user := m.From.Username
	if user == "" {
		user = strconv.FormatInt(m.From.ID, 10)
	}
	return inbound{
		ConversationID: "telegram:" + strconv.FormatInt(m.Chat.ID, 10),
		Message:        truncate(m.Text, maxMessageBytes),
		User:           user,
		Metadata:       map[string]any{"chat_id": m.Chat.ID, "message_id": m.MessageID},
	}, m.Chat.ID, true
}

// truncateUTF16 cuts s to at most n UTF-16 code units, which is how Telegram
// counts a message's length.
func truncateUTF16(s string, n int) string {
	units := 0
	for i, r := range s {
		units += utf16.RuneLen(r)
		if units > n {
			return s[:i]
		}
	}
	return s
}
