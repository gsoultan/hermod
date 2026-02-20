package linkedin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/user/hermod"
)

// LinkedInSink implements the hermod.Sink interface for LinkedIn.
type LinkedInSink struct {
	accessToken string
	personURN   string
	formatter   hermod.Formatter
	baseURL     string
}

// NewLinkedInSink creates a new LinkedInSink.
func NewLinkedInSink(accessToken, personURN string, formatter hermod.Formatter) *LinkedInSink {
	return &LinkedInSink{
		accessToken: accessToken,
		personURN:   personURN,
		formatter:   formatter,
		baseURL:     "https://api.linkedin.com/v2",
	}
}

// Write sends a post to LinkedIn.
func (s *LinkedInSink) Write(ctx context.Context, msg hermod.Message) error {
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

	// LinkedIn UGC Post structure
	body := map[string]any{
		"author":         s.personURN,
		"lifecycleState": "PUBLISHED",
		"specificContent": map[string]any{
			"com.linkedin.ugc.ShareContent": map[string]any{
				"shareCommentary": map[string]any{
					"text": text,
				},
				"shareMediaCategory": "NONE",
			},
		},
		"visibility": map[string]any{
			"com.linkedin.ugc.MemberNetworkVisibility": "PUBLIC",
		},
	}

	jsonBody, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, "POST", s.baseURL+"/ugcPosts", bytes.NewBuffer(jsonBody))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.accessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Restli-Protocol-Version", "2.0.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		var result map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&result)
		return fmt.Errorf("linkedin api returned status: %d, error: %v", resp.StatusCode, result["message"])
	}

	return nil
}

// WriteBatch sends multiple posts.
func (s *LinkedInSink) WriteBatch(ctx context.Context, msgs []hermod.Message) error {
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

// Ping checks the connection to LinkedIn API.
func (s *LinkedInSink) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", s.baseURL+"/me", nil)
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
		return fmt.Errorf("invalid linkedin token or connection error: %d", resp.StatusCode)
	}
	return nil
}

// Close closes the LinkedIn sink.
func (s *LinkedInSink) Close() error {
	return nil
}
