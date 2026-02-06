package instagram

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

// InstagramSource implements the hermod.Source interface for polling Instagram media.
type InstagramSource struct {
	accessToken  string
	igUserID     string
	interval     time.Duration
	sinceID      string
	client       *http.Client
	items        []map[string]interface{}
	currentIndex int
	lastPoll     time.Time
	baseURL      string
	mode         string // "media", "comments", "insights"
}

// NewInstagramSource creates a new InstagramSource.
func NewInstagramSource(accessToken, igUserID string, interval time.Duration, mode string) *InstagramSource {
	if interval == 0 {
		interval = 60 * time.Second
	}
	if mode == "" {
		mode = "media"
	}
	return &InstagramSource{
		accessToken: accessToken,
		igUserID:    igUserID,
		interval:    interval,
		mode:        mode,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		baseURL: "https://graph.facebook.com/v17.0",
	}
}

// Read reads the next media item from Instagram.
func (s *InstagramSource) Read(ctx context.Context) (hermod.Message, error) {
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
		// To get comments, we first get the media IDs
		apiURL = fmt.Sprintf("%s/%s/media?access_token=%s&fields=id&limit=5", s.baseURL, s.igUserID, s.accessToken)
		req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
		if err != nil {
			return nil, err
		}
		resp, err := s.client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		var mediaResult struct {
			Data []map[string]interface{} `json:"data"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&mediaResult); err != nil {
			return nil, err
		}
		var allComments []map[string]interface{}
		for _, m := range mediaResult.Data {
			mediaID := m["id"].(string)
			commentURL := fmt.Sprintf("%s/%s/comments?access_token=%s&fields=id,text,timestamp,username&limit=10", s.baseURL, mediaID, s.accessToken)
			req, _ := http.NewRequestWithContext(ctx, "GET", commentURL, nil)
			resp, err := s.client.Do(req)
			if err == nil {
				var commentResult struct {
					Data []map[string]interface{} `json:"data"`
				}
				if json.NewDecoder(resp.Body).Decode(&commentResult) == nil {
					for _, c := range commentResult.Data {
						c["media_id"] = mediaID
						allComments = append(allComments, c)
					}
				}
				resp.Body.Close()
			}
		}
		return s.processItems(ctx, allComments)

	case "insights":
		// Get user insights
		apiURL = fmt.Sprintf("%s/%s/insights?access_token=%s&metric=impressions,reach,profile_views&period=day", s.baseURL, s.igUserID, s.accessToken)
		req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
		if err != nil {
			return nil, err
		}
		resp, err := s.client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		var insightResult struct {
			Data []map[string]interface{} `json:"data"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&insightResult); err != nil {
			return nil, err
		}
		return s.processItems(ctx, insightResult.Data)

	default:
		apiURL = fmt.Sprintf("%s/%s/media?access_token=%s&fields=id,caption,media_type,media_url,permalink,timestamp,username&limit=20", s.baseURL, s.igUserID, s.accessToken)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errRes map[string]interface{}
		_ = json.NewDecoder(resp.Body).Decode(&errRes)
		return nil, fmt.Errorf("instagram api returned status %d: %v", resp.StatusCode, errRes["error"])
	}

	var result struct {
		Data []map[string]interface{} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return s.processItems(ctx, result.Data)
}

func (s *InstagramSource) processItems(ctx context.Context, data []map[string]interface{}) (hermod.Message, error) {
	var filtered []map[string]interface{}
	foundSince := false
	for _, item := range data {
		if s.sinceID != "" && item["id"] == s.sinceID {
			foundSince = true
			break
		}
		filtered = append(filtered, item)
	}

	if len(filtered) == 0 && (s.sinceID == "" || foundSince) {
		return s.Read(ctx)
	}

	// Reverse to process oldest first among the new items.
	for i, j := 0, len(filtered)-1; i < j; i, j = i+1, j-1 {
		filtered[i], filtered[j] = filtered[j], filtered[i]
	}

	s.items = filtered
	if len(data) > 0 {
		s.sinceID = data[0]["id"].(string)
	}

	item := s.items[0]
	s.currentIndex = 1
	return s.messageFromData(item), nil
}

func (s *InstagramSource) messageFromData(data map[string]interface{}) hermod.Message {
	msg := message.AcquireMessage()
	if id, ok := data["id"].(string); ok {
		msg.SetID(id)
	}
	msg.SetOperation(hermod.OpCreate)
	msg.SetMetadata("source", "instagram")
	msg.SetMetadata("ig_user_id", s.igUserID)
	msg.SetMetadata("mode", s.mode)

	for k, v := range data {
		msg.SetData(k, v)
	}

	if caption, ok := data["caption"].(string); ok {
		msg.SetPayload([]byte(caption))
	} else if text, ok := data["text"].(string); ok {
		msg.SetPayload([]byte(text))
	} else if name, ok := data["name"].(string); ok {
		msg.SetPayload([]byte(name))
	}

	return msg
}

// Ack acknowledges a message.
func (s *InstagramSource) Ack(ctx context.Context, msg hermod.Message) error {
	return nil
}

// Ping checks the connection to Instagram.
func (s *InstagramSource) Ping(ctx context.Context) error {
	apiURL := fmt.Sprintf("%s/%s?access_token=%s&fields=id,username", s.baseURL, s.igUserID, s.accessToken)
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("instagram token or ig_user_id invalid: %d", resp.StatusCode)
	}
	return nil
}

// GetState returns the current state of the source.
func (s *InstagramSource) GetState() map[string]string {
	return map[string]string{
		"since_id": s.sinceID,
	}
}

// SetState sets the current state of the source.
func (s *InstagramSource) SetState(state map[string]string) {
	if id, ok := state["since_id"]; ok {
		s.sinceID = id
	}
}

// Close closes the Instagram source.
func (s *InstagramSource) Close() error {
	return nil
}
