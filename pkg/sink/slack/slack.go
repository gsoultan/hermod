package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/user/hermod"
)

// SlackSink implements the hermod.Sink interface for Slack.
type SlackSink struct {
	webhookURL string
	token      string
	channelID  string
	formatter  hermod.Formatter
	baseURL    string // Added for testing
}

// NewSlackSink creates a new SlackSink.
func NewSlackSink(webhookURL, token, channelID string, formatter hermod.Formatter) *SlackSink {
	return &SlackSink{
		webhookURL: webhookURL,
		token:      token,
		channelID:  channelID,
		formatter:  formatter,
		baseURL:    "https://slack.com/api",
	}
}

// Write sends a message to Slack.
func (s *SlackSink) Write(ctx context.Context, msg hermod.Message) error {
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
	if text == "" {
		return nil
	}

	if s.webhookURL != "" {
		return s.sendWebhook(ctx, text)
	}

	if s.token != "" && s.channelID != "" {
		return s.sendBotMessage(ctx, text)
	}

	return fmt.Errorf("slack sink not configured: missing webhook_url or token/channel_id")
}

func (s *SlackSink) sendWebhook(ctx context.Context, text string) error {
	body, _ := json.Marshal(map[string]string{
		"text": text,
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

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("slack webhook returned status: %d", resp.StatusCode)
	}

	return nil
}

func (s *SlackSink) sendBotMessage(ctx context.Context, text string) error {
	apiURL := s.baseURL + "/chat.postMessage"
	body, _ := json.Marshal(map[string]string{
		"channel": s.channelID,
		"text":    text,
	})

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var result struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}

	if !result.OK {
		return fmt.Errorf("slack api error: %s", result.Error)
	}

	return nil
}

// WriteBatch sends multiple messages to Slack.
func (s *SlackSink) WriteBatch(ctx context.Context, msgs []hermod.Message) error {
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

// Ping checks the connection to Slack.
func (s *SlackSink) Ping(ctx context.Context) error {
	if s.webhookURL != "" {
		req, err := http.NewRequestWithContext(ctx, "HEAD", s.webhookURL, nil)
		if err != nil {
			return err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 && resp.StatusCode != http.StatusMethodNotAllowed {
			return fmt.Errorf("slack webhook ping failed: %d", resp.StatusCode)
		}
		return nil
	}

	if s.token != "" {
		apiURL := s.baseURL + "/auth.test"
		req, err := http.NewRequestWithContext(ctx, "POST", apiURL, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+s.token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		var result struct {
			OK bool `json:"ok"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&result)
		if !result.OK {
			return fmt.Errorf("slack token invalid")
		}
		return nil
	}

	return fmt.Errorf("slack sink not configured")
}

// Close closes the Slack sink.
func (s *SlackSink) Close() error {
	return nil
}
