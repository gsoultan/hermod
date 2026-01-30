package form

import (
	"context"
	"fmt"
	"sync"

	"github.com/user/hermod"
)

// Simple in-memory registry for form sources, similar to webhook source.
var (
	registry = make(map[string]chan hermod.Message)
	mu       sync.RWMutex
)

// Register creates a new channel for a form path.
func Register(path string) chan hermod.Message {
	mu.Lock()
	defer mu.Unlock()
	if ch, ok := registry[path]; ok {
		return ch
	}
	ch := make(chan hermod.Message, 1024)
	registry[path] = ch
	return ch
}

// Unregister closes and removes the channel for a form path.
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
	ch, ok := registry[path]
	mu.RUnlock()
	if !ok {
		return fmt.Errorf("no form registered for path: %s", path)
	}
	select {
	case ch <- msg:
		return nil
	default:
		return fmt.Errorf("form buffer full for path: %s", path)
	}
}

// FormSource implements the hermod.Source interface for receiving form submissions.
type FormSource struct {
	Path string
	ch   chan hermod.Message
}

// NewFormSource creates a new FormSource.
func NewFormSource(path string) *FormSource {
	if path == "" {
		path = "/api/forms/default"
	}
	return &FormSource{
		Path: path,
		ch:   Register(path),
	}
}

func (s *FormSource) Read(ctx context.Context) (hermod.Message, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case msg, ok := <-s.ch:
		if !ok {
			return nil, fmt.Errorf("form source closed")
		}
		return msg, nil
	}
}

func (s *FormSource) Ack(ctx context.Context, msg hermod.Message) error { return nil }
func (s *FormSource) Ping(ctx context.Context) error                    { return nil }
func (s *FormSource) Close() error {
	Unregister(s.Path)
	return nil
}
