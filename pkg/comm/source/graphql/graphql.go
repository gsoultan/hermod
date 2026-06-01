package graphql

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

// Register creates a new channel for a GraphQL path.
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

// Unregister closes and removes the channel for a GraphQL path.
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
		return fmt.Errorf("no GraphQL source registered for path: %s", path)
	}
	select {
	case ch <- msg:
		return nil
	default:
		return fmt.Errorf("GraphQL source buffer full for path: %s", path)
	}
}

// GraphQLSource implements the hermod.Source interface for receiving GraphQL requests.
type GraphQLSource struct {
	Path string
	ch   chan hermod.Message
}

// NewGraphQLSource creates a new GraphQLSource.
func NewGraphQLSource(path string) *GraphQLSource {
	if path == "" {
		path = "/api/graphql/default"
	}
	return &GraphQLSource{
		Path: path,
		ch:   Register(path),
	}
}

func (s *GraphQLSource) Read(ctx context.Context) (hermod.Message, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case msg, ok := <-s.ch:
		if !ok {
			return nil, fmt.Errorf("GraphQL source closed")
		}
		return msg, nil
	}
}

func (s *GraphQLSource) Ack(ctx context.Context, msg hermod.Message) error { return nil }
func (s *GraphQLSource) Ping(ctx context.Context) error                    { return nil }
func (s *GraphQLSource) Close() error {
	Unregister(s.Path)
	return nil
}
