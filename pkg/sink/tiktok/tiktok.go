package tiktok

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/user/hermod"
)

// TikTokSink implements the hermod.Sink interface for TikTok.
type TikTokSink struct {
	accessToken string
	formatter   hermod.Formatter
	baseURL     string
}

// NewTikTokSink creates a new TikTokSink.
func NewTikTokSink(accessToken string, formatter hermod.Formatter) *TikTokSink {
	return &TikTokSink{
		accessToken: accessToken,
		formatter:   formatter,
		baseURL:     "https://open.tiktokapis.com/v2",
	}
}

// Write publishes a video to TikTok.
func (s *TikTokSink) Write(ctx context.Context, msg hermod.Message) error {
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

	// For TikTok, we usually need a video_url and title in the data.
	// We'll try to extract them from msg.Data().
	msgData := msg.Data()
	videoURL, _ := msgData["video_url"].(string)
	title, _ := msgData["title"].(string)

	if videoURL == "" {
		// If no video_url in data, we can't publish a video.
		// However, as a sink we might just want to post the payload as title if possible,
		// but TikTok requires a video.
		return fmt.Errorf("tiktok sink requires 'video_url' in message data")
	}

	if title == "" {
		title = string(data)
	}

	body, _ := json.Marshal(map[string]interface{}{
		"source_info": map[string]string{
			"source":    "PULL_FROM_URL",
			"video_url": videoURL,
		},
		"video_metadata": map[string]string{
			"caption": title,
		},
	})

	apiURL := fmt.Sprintf("%s/post/publish/video/init/", s.baseURL)
	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		var result map[string]interface{}
		_ = json.NewDecoder(resp.Body).Decode(&result)
		return fmt.Errorf("tiktok api returned status: %d, error: %v", resp.StatusCode, result["error"])
	}

	return nil
}

// WriteBatch sends multiple videos.
func (s *TikTokSink) WriteBatch(ctx context.Context, msgs []hermod.Message) error {
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

// Ping checks the connection to TikTok API.
func (s *TikTokSink) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", s.baseURL+"/user/info/", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.accessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("invalid tiktok token or connection error: %d", resp.StatusCode)
	}
	return nil
}

// Close closes the TikTok sink.
func (s *TikTokSink) Close() error {
	return nil
}
