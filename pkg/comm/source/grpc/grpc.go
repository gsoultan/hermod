package grpcsource

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/google/uuid"
	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
	sourcebuf "github.com/gsoultan/hermod/pkg/comm/source"
	"github.com/gsoultan/hermod/pkg/comm/source/grpc/proto"
	"google.golang.org/grpc/metadata"
)

// paths routes what arrives for a path to the source that holds it. See
// sourcebuf.PathRegistry for who holds a path, and why a source that is built
// but never read does not take one from a source that is receiving.
var paths = sourcebuf.NewPathRegistry(sourcebuf.DefaultSourceBuffer)

// Register creates the channel a gRPC source reads its path from. It holds
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
		return fmt.Errorf("no gRPC source registered for path: %s", path)
	case errors.Is(err, sourcebuf.ErrPathBufferFull):
		return fmt.Errorf("gRPC source buffer full for path: %s", path)
	}
	return err
}

// GrpcSource implements the hermod.Source interface for receiving gRPC calls.
type GrpcSource struct {
	Path string
	ch   chan hermod.Message

	// reading is set by the first Read, which is when a source that was built
	// while another held its path takes the path over.
	reading atomic.Bool
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
	if !s.reading.Swap(true) {
		paths.TakeOver(s.Path, s.ch)
	}
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
