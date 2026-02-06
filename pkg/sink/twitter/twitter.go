package twitter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/user/hermod"
)

// TwitterSink implements the hermod.Sink interface for Twitter (X).
type TwitterSink struct {
	token     string
	formatter hermod.Formatter
	baseURL   string
}

// NewTwitterSink creates a new TwitterSink.
func NewTwitterSink(token string, formatter hermod.Formatter) *TwitterSink {
	return &TwitterSink{
		token:     token,
		formatter: formatter,
		baseURL:   "https://api.twitter.com/2",
	}
}

// Write sends a tweet to Twitter.
func (s *TwitterSink) Write(ctx context.Context, msg hermod.Message) error {
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

	// Twitter limit is 280 characters for standard accounts.
	if len(text) > 280 {
		text = text[:277] + "..."
	}

	body, _ := json.Marshal(map[string]string{
		"text": text,
	})

	req, err := http.NewRequestWithContext(ctx, "POST", s.baseURL+"/tweets", bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		var result map[string]interface{}
		_ = json.NewDecoder(resp.Body).Decode(&result)
		return fmt.Errorf("twitter api returned status: %d, detail: %v", resp.StatusCode, result["detail"])
	}

	return nil
}

// WriteBatch sends multiple tweets.
func (s *TwitterSink) WriteBatch(ctx context.Context, msgs []hermod.Message) error {
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

// Ping checks the connection to Twitter API.
func (s *TwitterSink) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", s.baseURL+"/users/me", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("invalid twitter token or connection error: %d", resp.StatusCode)
	}
	return nil
}

// Close closes the Twitter sink.
func (s *TwitterSink) Close() error {
	return nil
}
