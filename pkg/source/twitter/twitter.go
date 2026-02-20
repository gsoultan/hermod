package twitter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

// TwitterSource implements the hermod.Source interface for polling Twitter (X) tweets.
type TwitterSource struct {
	token        string
	query        string
	interval     time.Duration
	sinceID      string
	client       *http.Client
	items        []map[string]any
	currentIndex int
	lastPoll     time.Time
	baseURL      string
	mode         string // "search", "mentions", "metrics"
}

// NewTwitterSource creates a new TwitterSource.
func NewTwitterSource(token, query string, interval time.Duration, mode string) *TwitterSource {
	if interval == 0 {
		interval = 60 * time.Second
	}
	if mode == "" {
		mode = "search"
	}
	return &TwitterSource{
		token:    token,
		query:    query,
		interval: interval,
		mode:     mode,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		baseURL: "https://api.twitter.com/2",
	}
}

// Read reads the next tweet from Twitter.
func (s *TwitterSource) Read(ctx context.Context) (hermod.Message, error) {
	if s.currentIndex < len(s.items) {
		item := s.items[s.currentIndex]
		s.currentIndex++
		return s.messageFromData(item), nil
	}

	// Wait for interval
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
	case "mentions":
		// Get user ID first then mentions
		userReq, _ := http.NewRequestWithContext(ctx, "GET", s.baseURL+"/users/me", nil)
		userReq.Header.Set("Authorization", "Bearer "+s.token)
		userResp, err := s.client.Do(userReq)
		if err != nil {
			return nil, err
		}
		defer userResp.Body.Close()
		var userResult struct {
			Data struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if json.NewDecoder(userResp.Body).Decode(&userResult) != nil {
			return nil, fmt.Errorf("failed to get twitter user info")
		}
		apiURL = fmt.Sprintf("%s/users/%s/mentions?max_results=10", s.baseURL, userResult.Data.ID)

	case "metrics":
		// Get recent tweets and then their metrics
		apiURL = fmt.Sprintf("%s/tweets/search/recent?query=%s&max_results=10&tweet.fields=public_metrics", s.baseURL, url.QueryEscape(s.query))

	default:
		apiURL = fmt.Sprintf("%s/tweets/search/recent?query=%s&max_results=10", s.baseURL, url.QueryEscape(s.query))
	}

	if s.sinceID != "" && s.mode != "metrics" {
		apiURL = fmt.Sprintf("%s&since_id=%s", apiURL, s.sinceID)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+s.token)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errRes map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&errRes)
		return nil, fmt.Errorf("twitter api returned status %d: %v", resp.StatusCode, errRes["detail"])
	}

	var result struct {
		Data []map[string]any `json:"data"`
		Meta struct {
			NewestID string `json:"newest_id"`
		} `json:"meta"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	if len(result.Data) == 0 {
		return s.Read(ctx)
	}

	s.items = result.Data
	if result.Meta.NewestID != "" {
		s.sinceID = result.Meta.NewestID
	}

	item := s.items[0]
	s.currentIndex = 1
	return s.messageFromData(item), nil
}

func (s *TwitterSource) messageFromData(data map[string]any) hermod.Message {
	msg := message.AcquireMessage()
	msg.SetID(data["id"].(string))
	msg.SetOperation(hermod.OpCreate)
	msg.SetMetadata("source", "twitter")
	msg.SetMetadata("query", s.query)
	msg.SetMetadata("mode", s.mode)

	for k, v := range data {
		msg.SetData(k, v)
	}

	if text, ok := data["text"].(string); ok {
		msg.SetPayload([]byte(text))
	}

	return msg
}

// Ack acknowledges a message.
func (s *TwitterSource) Ack(ctx context.Context, msg hermod.Message) error {
	return nil
}

// Ping checks the connection to Twitter.
func (s *TwitterSource) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", s.baseURL+"/users/me", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("twitter token invalid: %d", resp.StatusCode)
	}
	return nil
}

// GetState returns the current state of the source.
func (s *TwitterSource) GetState() map[string]string {
	return map[string]string{
		"since_id": s.sinceID,
	}
}

// SetState sets the current state of the source.
func (s *TwitterSource) SetState(state map[string]string) {
	if id, ok := state["since_id"]; ok {
		s.sinceID = id
	}
}

// Close closes the Twitter source.
func (s *TwitterSource) Close() error {
	return nil
}
