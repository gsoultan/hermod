package grpcsource

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/google/uuid"
	hermod "github.com/gsoultan/Hermod"
	"github.com/gsoultan/Hermod/internal/storage"
	"github.com/gsoultan/Hermod/pkg/comm/message"
	sourcebuf "github.com/gsoultan/Hermod/pkg/comm/source"
	"github.com/gsoultan/Hermod/pkg/comm/source/grpc/proto"
	"google.golang.org/grpc/metadata"
)

var (
	registry = make(map[string]chan hermod.Message)
	mu       sync.RWMutex
)

// Register creates a new channel for a gRPC source path, superseding any existing
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
		return fmt.Errorf("no gRPC source registered for path: %s", path)
	}
	select {
	case ch <- msg:
		return nil
	default:
		return fmt.Errorf("gRPC source buffer full for path: %s", path)
	}
}

// GrpcSource implements the hermod.Source interface for receiving gRPC calls.
type GrpcSource struct {
	Path string
	ch   chan hermod.Message
}

// NewGrpcSource creates a new GrpcSource.
func NewGrpcSource(path string) *GrpcSource {
	if path == "" {
		path = "/grpc/default"
	}
	return &GrpcSource{
		Path: path,
		ch:   Register(path),
	}
}

func (s *GrpcSource) Read(ctx context.Context) (hermod.Message, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case msg, ok := <-s.ch:
		if !ok {
			return nil, errors.New("gRPC source closed")
		}
		return msg, nil
	}
}

func (s *GrpcSource) Ack(ctx context.Context, msg hermod.Message) error { return nil }
func (s *GrpcSource) Ping(ctx context.Context) error                    { return nil }
func (s *GrpcSource) Close() error {
	Unregister(s.Path, s.ch)
	return nil
}

// Server implements the proto.SourceServiceServer interface.
type Server struct {
	proto.UnimplementedSourceServiceServer
	Storage storage.Storage
}

func (s *Server) Publish(ctx context.Context, req *proto.PublishRequest) (*proto.PublishResponse, error) {
	path := req.Path
	if path == "" {
		path = "/grpc/default"
	}

	// Verify API Key if storage is available. A nil Storage means no key
	// store is wired at all — standalone use — and that is the only case
	// that skips the check. A store that exists but cannot be read fails
	// closed: skipping here turned a storage hiccup into anonymous ingress
	// on an endpoint the operator had put a key on.
	if s.Storage != nil {
		sources, _, err := s.Storage.ListSources(ctx, storage.CommonFilter{})
		if err != nil {
			return nil, fmt.Errorf("api key verification unavailable: %w", err)
		}
		var apiKey string
		for _, src := range sources {
			if src.Type == "grpc" && src.Config["path"] == path {
				apiKey = src.Config["api_key"]
				break
			}
		}

		if apiKey != "" {
			md, ok := metadata.FromIncomingContext(ctx)
			if !ok {
				return nil, errors.New("missing metadata")
			}
			tokens := md.Get("x-api-key")
			if len(tokens) == 0 || tokens[0] != apiKey {
				return nil, errors.New("invalid api key")
			}
		}
	}

	msg := message.AcquireMessage()
	if req.Id != "" {
		msg.SetID(req.Id)
	} else {
		// An empty ID reaches SQL sinks as an empty primary key, where every
		// anonymous record upserts the same row.
		msg.SetID(uuid.NewString())
	}
	msg.SetOperation(hermod.Operation(req.Operation))
	msg.SetTable(req.Table)
	msg.SetSchema(req.Schema)
	msg.SetBefore(req.Before)
	msg.SetAfter(req.After)
	msg.SetPayload(req.Payload)
	for k, v := range req.Metadata {
		msg.SetMetadata(k, v)
	}

	if err := Dispatch(path, msg); err != nil {
		message.ReleaseMessage(msg)
		return nil, err
	}

	return &proto.PublishResponse{
		Id:     msg.ID(),
		Status: "dispatched",
	}, nil
}
