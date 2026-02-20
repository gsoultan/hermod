package facebook

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

// FacebookSource implements the hermod.Source interface for polling Facebook Page feed.
type FacebookSource struct {
	accessToken  string
	pageID       string
	interval     time.Duration
	since        string // Unix timestamp
	client       *http.Client
	items        []map[string]any
	currentIndex int
	lastPoll     time.Time
	baseURL      string
	mode         string // "feed", "comments", "insights"
}

// NewFacebookSource creates a new FacebookSource.
func NewFacebookSource(accessToken, pageID string, interval time.Duration, mode string) *FacebookSource {
	if interval == 0 {
		interval = 60 * time.Second
	}
	if mode == "" {
		mode = "feed"
	}
	return &FacebookSource{
		accessToken: accessToken,
		pageID:      pageID,
		interval:    interval,
		mode:        mode,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		baseURL: "https://graph.facebook.com/v17.0",
	}
}

// Read reads the next post from Facebook.
func (s *FacebookSource) Read(ctx context.Context) (hermod.Message, error) {
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
		// Get comments for recent posts
		apiURL = fmt.Sprintf("%s/%s/feed?access_token=%s&fields=id&limit=5", s.baseURL, s.pageID, s.accessToken)
		req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
		if err != nil {
			return nil, err
		}
		resp, err := s.client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		var feedResult struct {
			Data []map[string]any `json:"data"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&feedResult); err != nil {
			return nil, err
		}
		var allComments []map[string]any
		for _, post := range feedResult.Data {
			postID := post["id"].(string)
			commentURL := fmt.Sprintf("%s/%s/comments?access_token=%s&fields=id,message,created_time,from&limit=10", s.baseURL, postID, s.accessToken)
			req, _ := http.NewRequestWithContext(ctx, "GET", commentURL, nil)
			resp, err := s.client.Do(req)
			if err == nil {
				var commentResult struct {
					Data []map[string]any `json:"data"`
				}
				if json.NewDecoder(resp.Body).Decode(&commentResult) == nil {
					for _, c := range commentResult.Data {
						c["post_id"] = postID
						allComments = append(allComments, c)
					}
				}
				resp.Body.Close()
			}
		}
		return s.processItems(ctx, allComments)

	case "insights":
		// Page insights
		apiURL = fmt.Sprintf("%s/%s/insights?access_token=%s&metric=page_impressions,page_engaged_users,page_views_total&period=day", s.baseURL, s.pageID, s.accessToken)
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
			Data []map[string]any `json:"data"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&insightResult); err != nil {
			return nil, err
		}
		return s.processItems(ctx, insightResult.Data)

	default:
		apiURL = fmt.Sprintf("%s/%s/feed?access_token=%s&limit=25", s.baseURL, s.pageID, s.accessToken)
		if s.since != "" {
			apiURL = fmt.Sprintf("%s&since=%s", apiURL, s.since)
		}
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
		var errRes map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&errRes)
		return nil, fmt.Errorf("facebook api returned status %d: %v", resp.StatusCode, errRes["error"])
	}

	var result struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return s.processItems(ctx, result.Data)
}

func (s *FacebookSource) processItems(ctx context.Context, data []map[string]any) (hermod.Message, error) {
	if len(data) == 0 {
		return s.Read(ctx)
	}

	s.items = data

	if s.mode == "feed" {
		if newest, ok := s.items[0]["created_time"].(string); ok {
			t, err := time.Parse(time.RFC3339, newest)
			if err == nil {
				s.since = strconv.FormatInt(t.Unix(), 10)
			}
		}
	}

	item := s.items[0]
	s.currentIndex = 1
	return s.messageFromData(item), nil
}

func (s *FacebookSource) messageFromData(data map[string]any) hermod.Message {
	msg := message.AcquireMessage()
	if id, ok := data["id"].(string); ok {
		msg.SetID(id)
	}
	msg.SetOperation(hermod.OpCreate)
	msg.SetMetadata("source", "facebook")
	msg.SetMetadata("page_id", s.pageID)
	msg.SetMetadata("mode", s.mode)

	for k, v := range data {
		msg.SetData(k, v)
	}

	if text, ok := data["message"].(string); ok {
		msg.SetPayload([]byte(text))
	} else if story, ok := data["story"].(string); ok {
		msg.SetPayload([]byte(story))
	} else if name, ok := data["name"].(string); ok {
		msg.SetPayload([]byte(name))
	}

	return msg
}

// Ack acknowledges a message.
func (s *FacebookSource) Ack(ctx context.Context, msg hermod.Message) error {
	return nil
}

// Ping checks the connection to Facebook.
func (s *FacebookSource) Ping(ctx context.Context) error {
	apiURL := fmt.Sprintf("%s/%s?access_token=%s&fields=id,name", s.baseURL, s.pageID, s.accessToken)
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
		return fmt.Errorf("facebook token or page_id invalid: %d", resp.StatusCode)
	}
	return nil
}

// GetState returns the current state of the source.
func (s *FacebookSource) GetState() map[string]string {
	return map[string]string{
		"since": s.since,
	}
}

// SetState sets the current state of the source.
func (s *FacebookSource) SetState(state map[string]string) {
	if since, ok := state["since"]; ok {
		s.since = since
	}
}

// Close closes the Facebook source.
func (s *FacebookSource) Close() error {
	return nil
}
