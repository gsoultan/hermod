package instagram

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/user/hermod"
)

// InstagramSink implements the hermod.Sink interface for Instagram.
type InstagramSink struct {
	accessToken string
	igUserID    string
	formatter   hermod.Formatter
	baseURL     string
}

// NewInstagramSink creates a new InstagramSink.
func NewInstagramSink(accessToken, igUserID string, formatter hermod.Formatter) *InstagramSink {
	return &InstagramSink{
		accessToken: accessToken,
		igUserID:    igUserID,
		formatter:   formatter,
		baseURL:     "https://graph.facebook.com/v17.0",
	}
}

// Write sends a post to Instagram. Requires media_url or image_url in message data.
func (s *InstagramSink) Write(ctx context.Context, msg hermod.Message) error {
	if msg == nil {
		return nil
	}

	mediaURL, ok := msg.Data()["media_url"].(string)
	if !ok {
		mediaURL, ok = msg.Data()["image_url"].(string)
	}

	if !ok || mediaURL == "" {
		return fmt.Errorf("instagram sink requires media_url or image_url in message data")
	}

	var caption string
	var err error
	if s.formatter != nil {
		data, err := s.formatter.Format(msg)
		if err != nil {
			return err
		}
		caption = string(data)
	} else {
		caption = string(msg.Payload())
	}

	// Step 1: Create media container
	apiURL := fmt.Sprintf("%s/%s/media", s.baseURL, s.igUserID)
	params := url.Values{}
	params.Set("image_url", mediaURL)
	params.Set("caption", caption)
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

	var result struct {
		ID    string `json:"id"`
		Error any    `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}

	if result.ID == "" {
		return fmt.Errorf("instagram create media error: %v", result.Error)
	}

	creationID := result.ID

	// Simple wait for media processing.
	// In production, one should poll the status of the container.
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(5 * time.Second):
	}

	// Step 2: Publish media
	publishURL := fmt.Sprintf("%s/%s/media_publish", s.baseURL, s.igUserID)
	params = url.Values{}
	params.Set("creation_id", creationID)
	params.Set("access_token", s.accessToken)

	req, err = http.NewRequestWithContext(ctx, "POST", publishURL+"?"+params.Encode(), nil)
	if err != nil {
		return err
	}
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		var resErr map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&resErr)
		return fmt.Errorf("instagram publish media error: %v", resErr["error"])
	}

	return nil
}

// WriteBatch sends multiple posts.
func (s *InstagramSink) WriteBatch(ctx context.Context, msgs []hermod.Message) error {
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

// Ping checks the connection to Instagram API.
func (s *InstagramSink) Ping(ctx context.Context) error {
	apiURL := fmt.Sprintf("%s/%s", s.baseURL, s.igUserID)
	params := url.Values{}
	params.Set("access_token", s.accessToken)
	params.Set("fields", "id,username")

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
		return fmt.Errorf("invalid instagram token or ig_user_id: %d", resp.StatusCode)
	}
	return nil
}

// Close closes the Instagram sink.
func (s *InstagramSink) Close() error {
	return nil
}
