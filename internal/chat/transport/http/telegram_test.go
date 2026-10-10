package http

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
)

// A chat source on the Telegram platform takes the Bot API's webhook updates,
// checks the secret token Telegram was given with setWebhook, and answers in
// the webhook's response with a sendMessage call: no outbound request, and so
// no bot token, is needed.

func telegramConfig() map[string]string {
	return map[string]string{"platform": "telegram", "telegram_secret_token": "tg-secret_1"}
}

func telegramSecret(v string) http.Header {
	return http.Header{"X-Telegram-Bot-Api-Secret-Token": {v}, "Content-Type": {"application/json"}}
}

const telegramUpdateBody = `{"update_id":10,"message":{"message_id":5,"from":{"id":77,"is_bot":false,"username":"ana"},` +
	`"chat":{"id":-100123,"type":"group"},"text":"hello bot"}}`

func TestATelegramChatAnswersInTheWebhookResponse(t *testing.T) {
	c := newChat(t, "tg", telegramConfig(), chatOptions{})
	rec := c.post(t, "/api/chat/tg", telegramUpdateBody, telegramSecret("tg-secret_1"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Method string `json:"method"`
		ChatID int64  `json:"chat_id"`
		Text   string `json:"text"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("answer is not JSON: %s", rec.Body.String())
	}
	if got.Method != "sendMessage" || got.ChatID != -100123 || got.Text != "echo: hello bot" {
		t.Errorf("answered %s", rec.Body.String())
	}
	data, meta := c.workflow.last(t)
	if data["conversation_id"] != "telegram:-100123" || data["user"] != "ana" {
		t.Errorf("the workflow saw %v", data)
	}
	if meta["chat_platform"] != "telegram" {
		t.Errorf("metadata %v", meta)
	}
}

func TestATelegramChatRefusesAnUpdateWithoutItsSecret(t *testing.T) {
	tests := []struct {
		name   string
		config map[string]string
		header http.Header
	}{
		{"no secret sent", telegramConfig(), nil},
		{"wrong secret", telegramConfig(), telegramSecret("guess")},
		{"no secret configured", map[string]string{"platform": "telegram"}, telegramSecret("")},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := "tg-auth-" + strconv.Itoa(i)
			c := newChat(t, path, tt.config, chatOptions{noEngine: true})
			if rec := c.post(t, "/api/chat/"+path, telegramUpdateBody, tt.header); rec.Code != http.StatusUnauthorized {
				t.Errorf("status %d, want 401: %s", rec.Code, rec.Body.String())
			}
			c.assertNothingDispatched(t)
		})
	}
}

// Updates that are not a person's text message are acknowledged, so Telegram
// does not redeliver them, and not answered.
func TestATelegramChatIgnoresWhatItShouldNotAnswer(t *testing.T) {
	tests := []struct{ name, body string }{
		{"a sticker", `{"update_id":11,"message":{"message_id":6,"from":{"id":77},"chat":{"id":1},"sticker":{}}}`},
		{"a bot", `{"update_id":12,"message":{"message_id":7,"from":{"id":78,"is_bot":true},"chat":{"id":1},"text":"hi"}}`},
		{"an edited message", `{"update_id":13,"edited_message":{"message_id":8,"from":{"id":77},"chat":{"id":1},"text":"hi"}}`},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := "tg-ignore-" + strconv.Itoa(i)
			c := newChat(t, path, telegramConfig(), chatOptions{noEngine: true})
			rec := c.post(t, "/api/chat/"+path, tt.body, telegramSecret("tg-secret_1"))
			if rec.Code != http.StatusOK {
				t.Errorf("status %d, want 200: %s", rec.Code, rec.Body.String())
			}
			c.assertNothingDispatched(t)
		})
	}
}
