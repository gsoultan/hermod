package webhook

import (
	"context"
	"fmt"
	"sync"

	"github.com/user/hermod"
)

var (
	registry = make(map[string]chan hermod.Message)
	mu       sync.RWMutex
)

// Register creates a new channel for a webhook path.
func Register(path string) chan hermod.Message {
	mu.Lock()
	defer mu.Unlock()
	ch := make(chan hermod.Message, 100)
	registry[path] = ch
	return ch
}

// Unregister closes and removes the channel for a webhook path.
func Unregister(path string) {
	mu.Lock()
	defer mu.Unlock()
	if ch, ok := registry[path]; ok {
		close(ch)
		delete(registry, path)
	}
}

// Dispatch sends a message to the channel registered for the given path.
func Dispatch(path string, msg hermod.Message) error {
	mu.RLock()
	defer mu.RUnlock()
	ch, ok := registry[path]
	if !ok {
		return fmt.Errorf("no webhook registered for path: %s", path)
	}
	select {
	case ch <- msg:
		return nil
	default:
		return fmt.Errorf("webhook buffer full for path: %s", path)
	}
}

// WebhookSource implements the hermod.Source interface for receiving HTTP requests.
type WebhookSource struct {
	Path string
	ch   chan hermod.Message
}

// NewWebhookSource creates a new WebhookSource.
func NewWebhookSource(path string) *WebhookSource {
	return &WebhookSource{
		Path: path,
		ch:   Register(path),
	}
}

// Read blocks until a message is received via Dispatch.
func (s *WebhookSource) Read(ctx context.Context) (hermod.Message, error) {
	select {
	case msg, ok := <-s.ch:
		if !ok {
			return nil, fmt.Errorf("webhook source closed")
		}
		return msg, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Ack is a no-op for webhooks.
func (s *WebhookSource) Ack(ctx context.Context, msg hermod.Message) error { return nil }

// Ping is a no-op for webhooks.
func (s *WebhookSource) Ping(ctx context.Context) error { return nil }

// Close unregisters the source.
func (s *WebhookSource) Close() error {
	Unregister(s.Path)
	return nil
}
