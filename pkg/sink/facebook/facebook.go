package facebook

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/user/hermod"
)

// FacebookSink implements the hermod.Sink interface for Facebook.
type FacebookSink struct {
	accessToken string
	pageID      string
	formatter   hermod.Formatter
	baseURL     string
}

// NewFacebookSink creates a new FacebookSink.
func NewFacebookSink(accessToken, pageID string, formatter hermod.Formatter) *FacebookSink {
	return &FacebookSink{
		accessToken: accessToken,
		pageID:      pageID,
		formatter:   formatter,
		baseURL:     "https://graph.facebook.com/v17.0",
	}
}

// Write sends a post to a Facebook Page feed.
func (s *FacebookSink) Write(ctx context.Context, msg hermod.Message) error {
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

	messageText := string(data)
	if messageText == "" {
		return nil
	}

	apiURL := fmt.Sprintf("%s/%s/feed", s.baseURL, s.pageID)
	params := url.Values{}
	params.Set("message", messageText)
	params.Set("access_token", s.accessToken)

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL+"?"+params.Encode(), nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		var result map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&result)
		return fmt.Errorf("facebook api returned status: %d, error: %v", resp.StatusCode, result["error"])
	}

	return nil
}

// WriteBatch sends multiple posts.
func (s *FacebookSink) WriteBatch(ctx context.Context, msgs []hermod.Message) error {
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

// Ping checks the connection to Facebook API.
func (s *FacebookSink) Ping(ctx context.Context) error {
	apiURL := fmt.Sprintf("%s/%s", s.baseURL, s.pageID)
	params := url.Values{}
	params.Set("access_token", s.accessToken)
	params.Set("fields", "id,name")

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL+"?"+params.Encode(), nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("invalid facebook token or page_id: %d", resp.StatusCode)
	}
	return nil
}

// Close closes the Facebook sink.
func (s *FacebookSink) Close() error {
	return nil
}
