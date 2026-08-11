package webhook

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/user/hermod"
)

var (
	registry = make(map[string]chan hermod.Message)
	mu       sync.RWMutex
)

// Register creates a new channel for a webhook path, superseding any existing
// registration. The newest registration owns the path: when a workflow moves
// between workers, the one taking the lease over is the one that should receive.
func Register(path string) chan hermod.Message {
	mu.Lock()
	defer mu.Unlock()
	ch := make(chan hermod.Message, 100)
	registry[path] = ch
	return ch
}

// Unregister releases a path, but only if ch is still the channel registered
// for it.
//
// The ownership check is what makes a handover safe. Nothing orders the
// outgoing worker's teardown against the incoming worker's registration, so
// deleting by path alone let a worker that had already lost the lease close and
// remove its successor's channel. The successor was then reading from a closed
// channel that no longer appeared in the registry: the workflow reported itself
// running and never received another message.
//
// A stale caller now finds its channel is no longer the registered one and
// leaves the path alone. Its own channel is simply dropped — its reader has
// already returned through the cancelled context, and nothing else holds it.
func Unregister(path string, ch chan hermod.Message) {
	mu.Lock()
	defer mu.Unlock()
	if current, ok := registry[path]; ok && current == ch {
		close(current)
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
			return nil, errors.New("webhook source closed")
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

// Close unregisters the source, unless another has already taken the path over.
func (s *WebhookSource) Close() error {
	Unregister(s.Path, s.ch)
	return nil
}
