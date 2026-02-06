package discord

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/user/hermod"
)

// DiscordSink implements the hermod.Sink interface for Discord.
type DiscordSink struct {
	webhookURL string
	token      string
	channelID  string
	formatter  hermod.Formatter
	baseURL    string // Added for testing
}

// NewDiscordSink creates a new DiscordSink.
func NewDiscordSink(webhookURL, token, channelID string, formatter hermod.Formatter) *DiscordSink {
	return &DiscordSink{
		webhookURL: webhookURL,
		token:      token,
		channelID:  channelID,
		formatter:  formatter,
		baseURL:    "https://discord.com/api/v10",
	}
}

// Write sends a message to Discord.
func (s *DiscordSink) Write(ctx context.Context, msg hermod.Message) error {
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

	content := string(data)
	if content == "" {
		return nil
	}

	// Discord content limit is 2000 characters
	if len(content) > 2000 {
		content = content[:1997] + "..."
	}

	if s.webhookURL != "" {
		return s.sendWebhook(ctx, content)
	}

	if s.token != "" && s.channelID != "" {
		return s.sendBotMessage(ctx, content)
	}

	return fmt.Errorf("discord sink not configured: missing webhook_url or token/channel_id")
}

func (s *DiscordSink) sendWebhook(ctx context.Context, content string) error {
	body, _ := json.Marshal(map[string]string{
		"content": content,
	})

	req, err := http.NewRequestWithContext(ctx, "POST", s.webhookURL, bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("discord webhook returned status: %d", resp.StatusCode)
	}

	return nil
}

func (s *DiscordSink) sendBotMessage(ctx context.Context, content string) error {
	apiURL := fmt.Sprintf("%s/channels/%s/messages", s.baseURL, s.channelID)
	body, _ := json.Marshal(map[string]string{
		"content": content,
	})

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bot "+s.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("discord bot api returned status: %d", resp.StatusCode)
	}

	return nil
}

// WriteBatch sends multiple messages to Discord.
func (s *DiscordSink) WriteBatch(ctx context.Context, msgs []hermod.Message) error {
	for _, msg := range msgs {
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

// Ping checks the connection to Discord.
func (s *DiscordSink) Ping(ctx context.Context) error {
	if s.webhookURL != "" {
		req, err := http.NewRequestWithContext(ctx, "GET", s.webhookURL, nil)
		if err != nil {
			return err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		// Webhook GET usually returns 200 with some metadata
		if resp.StatusCode >= 400 && resp.StatusCode != http.StatusMethodNotAllowed {
			return fmt.Errorf("discord webhook ping failed with status: %d", resp.StatusCode)
		}
		return nil
	}

	if s.token != "" {
		req, err := http.NewRequestWithContext(ctx, "GET", s.baseURL+"/users/@me", nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bot "+s.token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("discord bot token invalid: %d", resp.StatusCode)
		}
		return nil
	}

	return fmt.Errorf("discord sink not configured")
}

// Close closes the Discord sink.
func (s *DiscordSink) Close() error {
	return nil
}
