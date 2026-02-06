package linkedin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

// LinkedInSource implements the hermod.Source interface for polling LinkedIn UGC posts.
type LinkedInSource struct {
	accessToken  string
	personURN    string
	interval     time.Duration
	sinceID      string
	client       *http.Client
	items        []map[string]interface{}
	currentIndex int
	lastPoll     time.Time
	baseURL      string
}

// NewLinkedInSource creates a new LinkedInSource.
func NewLinkedInSource(accessToken, personURN string, interval time.Duration) *LinkedInSource {
	if interval == 0 {
		interval = 60 * time.Second
	}
	return &LinkedInSource{
		accessToken: accessToken,
		personURN:   personURN,
		interval:    interval,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		baseURL: "https://api.linkedin.com/v2",
	}
}

// Read reads the next post from LinkedIn.
func (s *LinkedInSource) Read(ctx context.Context) (hermod.Message, error) {
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

	apiURL := fmt.Sprintf("%s/ugcPosts?q=author&author=%s&count=20", s.baseURL, s.personURN)

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+s.accessToken)
	req.Header.Set("X-Restli-Protocol-Version", "2.0.0")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errRes map[string]interface{}
		_ = json.NewDecoder(resp.Body).Decode(&errRes)
		return nil, fmt.Errorf("linkedin api returned status %d: %v", resp.StatusCode, errRes["message"])
	}

	var result struct {
		Elements []map[string]interface{} `json:"elements"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	var filtered []map[string]interface{}
	foundSince := false
	for _, item := range result.Elements {
		if s.sinceID != "" && item["id"] == s.sinceID {
			foundSince = true
			break
		}
		filtered = append(filtered, item)
	}

	if len(filtered) == 0 && (s.sinceID == "" || foundSince) {
		return s.Read(ctx)
	}

	// LinkedIn returns newest first usually.
	s.items = filtered
	if len(result.Elements) > 0 {
		s.sinceID = result.Elements[0]["id"].(string)
	}

	item := s.items[0]
	s.currentIndex = 1
	return s.messageFromData(item), nil
}

func (s *LinkedInSource) messageFromData(data map[string]interface{}) hermod.Message {
	msg := message.AcquireMessage()
	if id, ok := data["id"].(string); ok {
		msg.SetID(id)
	}
	msg.SetOperation(hermod.OpCreate)
	msg.SetMetadata("source", "linkedin")
	msg.SetMetadata("author", s.personURN)

	for k, v := range data {
		msg.SetData(k, v)
	}

	// Try to extract text from UGC post structure
	if specificContent, ok := data["specificContent"].(map[string]interface{}); ok {
		if shareContent, ok := specificContent["com.linkedin.ugc.ShareContent"].(map[string]interface{}); ok {
			if shareCommentary, ok := shareContent["shareCommentary"].(map[string]interface{}); ok {
				if text, ok := shareCommentary["text"].(string); ok {
					msg.SetPayload([]byte(text))
				}
			}
		}
	}

	return msg
}

// Ack acknowledges a message.
func (s *LinkedInSource) Ack(ctx context.Context, msg hermod.Message) error {
	return nil
}

// Ping checks the connection to LinkedIn.
func (s *LinkedInSource) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", s.baseURL+"/me", nil)
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
		return fmt.Errorf("linkedin token invalid: %d", resp.StatusCode)
	}
	return nil
}

// GetState returns the current state of the source.
func (s *LinkedInSource) GetState() map[string]string {
	return map[string]string{
		"since_id": s.sinceID,
	}
}

// SetState sets the current state of the source.
func (s *LinkedInSource) SetState(state map[string]string) {
	if id, ok := state["since_id"]; ok {
		s.sinceID = id
	}
}

// Close closes the LinkedIn source.
func (s *LinkedInSource) Close() error {
	return nil
}
