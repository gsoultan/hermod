package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

// SlackSource implements the hermod.Source interface for polling Slack messages.
type SlackSource struct {
	token         string
	channelID     string
	interval      time.Duration
	lastTimestamp string
	client        *http.Client
	items         []map[string]interface{}
	currentIndex  int
	lastPoll      time.Time
	baseURL       string // Added for testing
}

// NewSlackSource creates a new SlackSource.
func NewSlackSource(token, channelID string, interval time.Duration) *SlackSource {
	if interval == 0 {
		interval = 10 * time.Second
	}
	return &SlackSource{
		token:     token,
		channelID: channelID,
		interval:  interval,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		baseURL: "https://slack.com/api",
	}
}

// Read reads the next message from Slack.
func (s *SlackSource) Read(ctx context.Context) (hermod.Message, error) {
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

	apiURL := fmt.Sprintf("%s/conversations.history?channel=%s&limit=50", s.baseURL, s.channelID)
	if s.lastTimestamp != "" {
		apiURL = fmt.Sprintf("%s&oldest=%s", apiURL, s.lastTimestamp)
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

	var result struct {
		OK       bool                     `json:"ok"`
		Messages []map[string]interface{} `json:"messages"`
		Error    string                   `json:"error"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	if !result.OK {
		return nil, fmt.Errorf("slack api error: %s", result.Error)
	}

	if len(result.Messages) == 0 {
		// No new messages, wait and try again
		return s.Read(ctx)
	}

	// Slack returns messages newest first. We want oldest first for sequential processing.
	for i, j := 0, len(result.Messages)-1; i < j; i, j = i+1, j-1 {
		result.Messages[i], result.Messages[j] = result.Messages[j], result.Messages[i]
	}

	s.items = result.Messages
	s.lastTimestamp = s.items[len(s.items)-1]["ts"].(string)

	item := s.items[0]
	s.currentIndex = 1
	return s.messageFromData(item), nil
}

func (s *SlackSource) messageFromData(data map[string]interface{}) hermod.Message {
	msg := message.AcquireMessage()
	msg.SetID(data["ts"].(string))
	msg.SetOperation(hermod.OpCreate)
	msg.SetMetadata("source", "slack")
	msg.SetMetadata("channel_id", s.channelID)

	if user, ok := data["user"].(string); ok {
		msg.SetMetadata("user_id", user)
	}

	for k, v := range data {
		msg.SetData(k, v)
	}

	if text, ok := data["text"].(string); ok {
		msg.SetPayload([]byte(text))
	}

	return msg
}

// Ack acknowledges a message.
func (s *SlackSource) Ack(ctx context.Context, msg hermod.Message) error {
	return nil
}

// Ping checks the connection to Slack.
func (s *SlackSource) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "POST", s.baseURL+"/auth.test", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	resp, err := s.client.Do(req)
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

// GetState returns the current state of the source.
func (s *SlackSource) GetState() map[string]string {
	return map[string]string{
		"last_timestamp": s.lastTimestamp,
	}
}

// SetState sets the current state of the source.
func (s *SlackSource) SetState(state map[string]string) {
	if ts, ok := state["last_timestamp"]; ok {
		s.lastTimestamp = ts
	}
}

// Close closes the Slack source.
func (s *SlackSource) Close() error {
	return nil
}
