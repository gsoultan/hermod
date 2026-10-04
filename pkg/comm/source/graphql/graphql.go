package graphql

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
var paths = sourcebuf.NewPathRegistry(sourcebuf.DefaultSourceBuffer)

// Register creates the channel a GraphQL source reads its path from. It holds
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
		return fmt.Errorf("no GraphQL source registered for path: %s", path)
	case errors.Is(err, sourcebuf.ErrPathBufferFull):
		return fmt.Errorf("GraphQL source buffer full for path: %s", path)
	}
	return err
}

// GraphQLSource implements the hermod.Source interface for receiving GraphQL requests.
type GraphQLSource struct {
	Path string
	ch   chan hermod.Message

	// reading is set by the first Read, which is when a source that was built
	// while another held its path takes the path over.
	reading atomic.Bool
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
	if !s.reading.Swap(true) {
		paths.TakeOver(s.Path, s.ch)
	}
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
