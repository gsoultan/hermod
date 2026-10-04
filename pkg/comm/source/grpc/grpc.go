package grpcsource

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/google/uuid"
	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
	sourcebuf "github.com/gsoultan/hermod/pkg/comm/source"
	"github.com/gsoultan/hermod/pkg/comm/source/grpc/proto"
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

// defaultPath is where a source configured without a path listens, and where a
// request sent without one is delivered.
const defaultPath = "/grpc/default"

// NewGrpcSource creates a new GrpcSource.
func NewGrpcSource(path string) *GrpcSource {
	if path == "" {
		path = defaultPath
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

	// Storage is the store holding the sources' API keys, for a caller whose
	// store never changes.
	Storage storage.Storage

	// StorageFunc, when set, is asked for that store on every Publish and takes
	// precedence over Storage. The API server needs this: its store is nil
	// until first-time setup opens a database and is replaced again by a
	// database switch, and a copy taken when the listener started never saw
	// either — so a fresh install checked no keys at all until it was restarted.
	StorageFunc func() storage.Storage
}

// keyStore returns the store to check API keys against right now.
func (s *Server) keyStore() storage.Storage {
	if s.StorageFunc != nil {
		return s.StorageFunc()
	}
	return s.Storage
}

func (s *Server) Publish(ctx context.Context, req *proto.PublishRequest) (*proto.PublishResponse, error) {
	path := req.Path
	if path == "" {
		path = defaultPath
	}

	// Verify API Key if storage is available. A nil store means no key
	// store is wired at all — standalone use, or an install that has not been
	// set up and so has no sources either — and that is the only case
	// that skips the check. A store that exists but cannot be read fails
	// closed: skipping here turned a storage hiccup into anonymous ingress
	// on an endpoint the operator had put a key on.
	if store := s.keyStore(); store != nil {
		sources, _, err := store.ListSources(ctx, storage.CommonFilter{})
		if err != nil {
			return nil, fmt.Errorf("api key verification unavailable: %w", err)
		}
		var apiKey string
		var known bool
		for _, src := range sources {
			if src.Type != "grpc" {
				continue
			}
			// A source saved without a path listens on the default one.
			stored := src.Config["path"]
			if stored == "" {
				stored = defaultPath
			}
			if stored == path {
				apiKey = src.Config["api_key"]
				known = true
				break
			}
		}
		// A path the store holds no source for has no key to check, and that
		// must not read as "no key required". The store and the running
		// workflows can disagree — a database switch leaves a workflow running
		// whose source the new database has never held — and a keyed path
		// would then take publishes with no key at all.
		if !known {
			return nil, fmt.Errorf("no gRPC source is configured for path: %s", path)
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
	// The message has one body: SetAfter is SetPayload under another name.
	// Setting both in turn let an empty payload erase the after-image a
	// producer had sent instead, so the row is taken from whichever is present,
	// payload first.
	body := req.Payload
	if len(body) == 0 {
		body = req.After
	}
	msg.SetPayload(body)
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
