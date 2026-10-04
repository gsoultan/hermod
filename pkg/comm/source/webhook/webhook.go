package webhook

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/gsoultan/hermod"
	sourcebuf "github.com/gsoultan/hermod/pkg/comm/source"
)

// paths routes what arrives for a path to the source that holds it. See
// sourcebuf.PathRegistry for who holds a path, and why a source that is built
// but never read does not take one from a source that is receiving.
var paths = sourcebuf.NewPathRegistry(100)

// Register creates the channel a webhook source reads its path from. It holds
// the path if nothing else does, and otherwise waits to take it over.
func Register(path string) chan hermod.Message {
	return paths.Register(path)
}

// Unregister releases a channel's claim on a path. Only the channel that holds
// the path removes it, so an outgoing engine's teardown cannot remove the
// source that took over from it.
func Unregister(path string, ch chan hermod.Message) {
	paths.Unregister(path, ch)
}

// Dispatch sends a message to the source that holds the given path.
func Dispatch(path string, msg hermod.Message) error {
	err := paths.Dispatch(path, msg)
	switch {
	case errors.Is(err, sourcebuf.ErrPathNotRegistered):
		return fmt.Errorf("no webhook registered for path: %s", path)
	case errors.Is(err, sourcebuf.ErrPathBufferFull):
		return fmt.Errorf("webhook buffer full for path: %s", path)
	}
	return err
}

// WebhookSource implements the hermod.Source interface for receiving HTTP requests.
type WebhookSource struct {
	Path string
	ch   chan hermod.Message

	// reading is set by the first Read, which is when a source that was built
	// while another held its path takes the path over.
	reading atomic.Bool
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
	if !s.reading.Swap(true) {
		paths.TakeOver(s.Path, s.ch)
	}
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
