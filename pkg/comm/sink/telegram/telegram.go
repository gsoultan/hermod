package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/user/hermod"
)

// TelegramSink implements the hermod.Sink interface for Telegram.
type TelegramSink struct {
	token     string
	chatID    string
	formatter hermod.Formatter
}

// NewTelegramSink creates a new TelegramSink.
func NewTelegramSink(token, chatID string, formatter hermod.Formatter) *TelegramSink {
	return &TelegramSink{
		token:     token,
		chatID:    chatID,
		formatter: formatter,
	}
}

// Write sends a message to Telegram.
func (s *TelegramSink) Write(ctx context.Context, msg hermod.Message) error {
	if msg == nil {
		return nil
	}
	var data []byte
	var err error

	if s.formatter != nil {
		data, err = s.formatter.Format(msg)
	} else {
		data = msg.Payload()
	}

	if err != nil {
		return fmt.Errorf("failed to format message: %w", err)
	}

	text := string(data)
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", s.token)
	body, _ := json.Marshal(map[string]string{
		"chat_id":    s.chatID,
		"text":       text,
		"parse_mode": "Markdown",
	})

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var result map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&result)
		return fmt.Errorf("telegram api returned status: %d, error: %v", resp.StatusCode, result["description"])
	}

	return nil
}

func (s *TelegramSink) WriteBatch(ctx context.Context, msgs []hermod.Message) error {
	for _, msg := range msgs {
		if msg == nil {
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			if err := s.Write(ctx, msg); err != nil {
				return err
			}
		}
	}
	return nil
}

// Ping checks the connection to Telegram API.
func (s *TelegramSink) Ping(ctx context.Context) error {
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/getMe", s.token)
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("invalid telegram token or connection error: %d", resp.StatusCode)
	}
	return nil
}

// Close closes the Telegram sink.
func (s *TelegramSink) Close() error {
	return nil
}
