package grpcsource

import (
	"context"
	"fmt"
	"sync"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/message"
	"github.com/user/hermod/pkg/source/grpc/proto"
	"google.golang.org/grpc/metadata"
)

var (
	registry = make(map[string]chan hermod.Message)
	mu       sync.RWMutex
)

// Register creates a new channel for a gRPC path.
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

// Unregister closes and removes the channel for a gRPC path.
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
			return nil, fmt.Errorf("gRPC source closed")
		}
		return msg, nil
	}
}

func (s *GrpcSource) Ack(ctx context.Context, msg hermod.Message) error { return nil }
func (s *GrpcSource) Ping(ctx context.Context) error                    { return nil }
func (s *GrpcSource) Close() error {
	Unregister(s.Path)
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

	// Verify API Key if storage is available
	if s.Storage != nil {
		sources, _, err := s.Storage.ListSources(ctx, storage.CommonFilter{})
		if err == nil {
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
					return nil, fmt.Errorf("missing metadata")
				}
				tokens := md.Get("x-api-key")
				if len(tokens) == 0 || tokens[0] != apiKey {
					return nil, fmt.Errorf("invalid api key")
				}
			}
		}
	}

	msg := message.AcquireMessage()
	if req.Id != "" {
		msg.SetID(req.Id)
	} else {
		// ID will be generated if missing
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
