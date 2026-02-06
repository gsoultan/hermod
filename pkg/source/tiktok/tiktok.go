package tiktok

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

// TikTokSource implements the hermod.Source interface for polling TikTok videos.
type TikTokSource struct {
	accessToken  string
	interval     time.Duration
	cursor       int64
	client       *http.Client
	items        []map[string]interface{}
	currentIndex int
	lastPoll     time.Time
	baseURL      string
	mode         string // "videos", "comments", "statistics"
}

// NewTikTokSource creates a new TikTokSource.
func NewTikTokSource(accessToken string, interval time.Duration, mode string) *TikTokSource {
	if interval == 0 {
		interval = 60 * time.Second
	}
	if mode == "" {
		mode = "videos"
	}
	return &TikTokSource{
		accessToken: accessToken,
		interval:    interval,
		mode:        mode,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		baseURL: "https://open.tiktokapis.com/v2",
	}
}

// Read reads the next item from TikTok.
func (s *TikTokSource) Read(ctx context.Context) (hermod.Message, error) {
	if s.currentIndex < len(s.items) {
		item := s.items[s.currentIndex]
		s.currentIndex++
		return s.messageFromData(item), nil
	}

	if !s.lastPoll.IsZero() {
		nextRun := s.lastPoll.Add(s.interval)
		if time.Now().Before(nextRun) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Until(nextRun)):
			}
		}
	}

	s.lastPoll = time.Now()
	s.items = nil
	s.currentIndex = 0

	var apiURL string
	switch s.mode {
	case "comments":
		// Mock/Generic endpoint for comments
		apiURL = fmt.Sprintf("%s/video/comments/list/?access_token=%s", s.baseURL, s.accessToken)
	case "statistics":
		// Mock/Generic endpoint for user statistics
		apiURL = fmt.Sprintf("%s/user/stats/?access_token=%s", s.baseURL, s.accessToken)
	default:
		// Default: video list
		apiURL = fmt.Sprintf("%s/video/list/?access_token=%s", s.baseURL, s.accessToken)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+s.accessToken)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errRes map[string]interface{}
		_ = json.NewDecoder(resp.Body).Decode(&errRes)
		return nil, fmt.Errorf("tiktok api returned status %d: %v", resp.StatusCode, errRes["error"])
	}

	var result struct {
		Data struct {
			Videos []map[string]interface{} `json:"videos"`
			Cursor int64                    `json:"cursor"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	if len(result.Data.Videos) == 0 {
		return s.Read(ctx)
	}

	s.items = result.Data.Videos
	s.cursor = result.Data.Cursor

	item := s.items[0]
	s.currentIndex = 1
	return s.messageFromData(item), nil
}

func (s *TikTokSource) messageFromData(data map[string]interface{}) hermod.Message {
	msg := message.AcquireMessage()
	if id, ok := data["id"].(string); ok {
		msg.SetID(id)
	}
	msg.SetOperation(hermod.OpCreate)
	msg.SetMetadata("source", "tiktok")
	msg.SetMetadata("mode", s.mode)

	for k, v := range data {
		msg.SetData(k, v)
	}

	if title, ok := data["title"].(string); ok {
		msg.SetPayload([]byte(title))
	}

	return msg
}

// Ack acknowledges a message.
func (s *TikTokSource) Ack(ctx context.Context, msg hermod.Message) error {
	return nil
}

// Ping checks the connection to TikTok.
func (s *TikTokSource) Ping(ctx context.Context) error {
	apiURL := fmt.Sprintf("%s/user/info/", s.baseURL)
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.accessToken)
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("tiktok token invalid: %d", resp.StatusCode)
	}
	return nil
}

// GetState returns the current state of the source.
func (s *TikTokSource) GetState() map[string]string {
	return map[string]string{
		"cursor": fmt.Sprintf("%d", s.cursor),
	}
}

// SetState sets the current state of the source.
func (s *TikTokSource) SetState(state map[string]string) {
	if cursor, ok := state["cursor"]; ok {
		fmt.Sscanf(cursor, "%d", &s.cursor)
	}
}

// Close closes the TikTok source.
func (s *TikTokSource) Close() error {
	return nil
}
