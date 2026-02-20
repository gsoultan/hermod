package discord

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

// DiscordSource implements the hermod.Source interface for polling Discord messages.
type DiscordSource struct {
	token         string
	channelID     string
	interval      time.Duration
	lastMessageID string
	client        *http.Client
	items         []map[string]any
	currentIndex  int
	lastPoll      time.Time
	baseURL       string // Added for testing
}

// NewDiscordSource creates a new DiscordSource.
func NewDiscordSource(token, channelID string, interval time.Duration) *DiscordSource {
	if interval == 0 {
		interval = 10 * time.Second
	}
	return &DiscordSource{
		token:     token,
		channelID: channelID,
		interval:  interval,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		baseURL: "https://discord.com/api/v10",
	}
}

// Read reads the next message from Discord.
func (s *DiscordSource) Read(ctx context.Context) (hermod.Message, error) {
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

	apiURL := fmt.Sprintf("%s/channels/%s/messages?limit=50", s.baseURL, s.channelID)
	if s.lastMessageID != "" {
		apiURL = fmt.Sprintf("%s&after=%s", apiURL, s.lastMessageID)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bot "+s.token)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("discord api returned status %d: %s", resp.StatusCode, string(body))
	}

	var newMessages []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&newMessages); err != nil {
		return nil, err
	}

	if len(newMessages) == 0 {
		// No new messages, wait and try again
		return s.Read(ctx)
	}

	// Discord returns messages in reverse chronological order normally,
	// but when using 'after', it returns them in chronological order (oldest first).
	s.items = newMessages
	s.lastMessageID = s.items[len(s.items)-1]["id"].(string)

	item := s.items[0]
	s.currentIndex = 1
	return s.messageFromData(item), nil
}

func (s *DiscordSource) messageFromData(data map[string]any) hermod.Message {
	msg := message.AcquireMessage()
	msg.SetID(data["id"].(string))
	msg.SetOperation(hermod.OpCreate)
	msg.SetMetadata("source", "discord")
	msg.SetMetadata("channel_id", s.channelID)

	if author, ok := data["author"].(map[string]any); ok {
		msg.SetMetadata("author_id", author["id"].(string))
		msg.SetMetadata("author_username", author["username"].(string))
	}

	for k, v := range data {
		msg.SetData(k, v)
	}

	if content, ok := data["content"].(string); ok {
		msg.SetPayload([]byte(content))
	}

	return msg
}

// Ack acknowledges a message.
func (s *DiscordSource) Ack(ctx context.Context, msg hermod.Message) error {
	return nil
}

// Ping checks the connection to Discord.
func (s *DiscordSource) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", s.baseURL+"/users/@me", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bot "+s.token)
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("discord bot token invalid: %d", resp.StatusCode)
	}
	return nil
}

// GetState returns the current state of the source.
func (s *DiscordSource) GetState() map[string]string {
	return map[string]string{
		"last_message_id": s.lastMessageID,
	}
}

// SetState sets the current state of the source.
func (s *DiscordSource) SetState(state map[string]string) {
	if id, ok := state["last_message_id"]; ok {
		s.lastMessageID = id
	}
}

// Close closes the Discord source.
func (s *DiscordSource) Close() error {
	return nil
}
