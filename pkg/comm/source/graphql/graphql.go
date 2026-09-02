package graphql

import (
	"context"
	"errors"
	"fmt"
	"sync"

	hermod "github.com/gsoultan/Hermod"
	sourcebuf "github.com/gsoultan/Hermod/pkg/comm/source"
)

var (
	registry = make(map[string]chan hermod.Message)
	mu       sync.RWMutex
)

// Register creates a new channel for a GraphQL source path, superseding any existing
// registration. The newest registration owns the path: when a workflow moves
// between workers, the one taking the lease over is the one that should receive.
//
// It used to return the existing channel instead, which meant the worker taking
// over and the worker being replaced read from the same one — so the outgoing
// teardown closed the channel its successor was reading.
func Register(path string) chan hermod.Message {
	mu.Lock()
	defer mu.Unlock()
	ch := make(chan hermod.Message, sourcebuf.DefaultSourceBuffer)
	registry[path] = ch
	return ch
}

// Unregister releases a path, but only if ch is still the channel registered
// for it.
//
// The ownership check is what makes a handover safe. Nothing orders the outgoing
// worker's teardown against the incoming worker's registration, so deleting by
// path alone let a worker that had already lost the lease close and remove its
// successor's channel. The successor was then reading from a closed channel that
// no longer appeared in the registry: the workflow reported itself running and
// never received another message.
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
			return nil, errors.New("GraphQL source closed")
		}
		return msg, nil
	}
}

func (s *GraphQLSource) Ack(ctx context.Context, msg hermod.Message) error { return nil }
func (s *GraphQLSource) Ping(ctx context.Context) error                    { return nil }
func (s *GraphQLSource) Close() error {
	Unregister(s.Path, s.ch)
	return nil
}
