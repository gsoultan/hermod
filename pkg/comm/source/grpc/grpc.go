package grpcsource

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/comm/reply"
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
	config, err := s.authorize(ctx, path)
	if err != nil {
		return nil, err
	}

	id, pending, timeout, err := s.enqueue(path, req, config)
	if err != nil {
		return nil, err
	}
	if pending == nil {
		return &proto.PublishResponse{Id: id, Status: "dispatched"}, nil
	}
	defer pending.Cancel()
	return await(ctx, id, pending, timeout), nil
}

// authorize checks a caller against the source configured for path and returns
// that source's configuration, which says how the source answers. It is nil
// when no store is wired, and the source then answers once the record is queued.
//
// A nil store means no key store is wired at all — standalone use, or an
// install that has not been set up and so has no sources either — and that is
// the only case that skips the check. A store that exists but cannot be read
// fails closed.
func (s *Server) authorize(ctx context.Context, path string) (map[string]string, error) {
	store := s.keyStore()
	if store == nil {
		return nil, nil
	}
	sources, _, err := store.ListSources(ctx, storage.CommonFilter{})
	if err != nil {
		return nil, fmt.Errorf("api key verification unavailable: %w", err)
	}
	// A path the store holds no source for has no key to check, and that
	// must not read as "no key required". The store and the running
	// workflows can disagree — a database switch leaves a workflow running
	// whose source the new database has never held — and a keyed path
	// would then take publishes with no key at all.
	config, known := sourceConfigFor(sources, path)
	if !known {
		return nil, fmt.Errorf("no gRPC source is configured for path: %s", path)
	}
	if err := checkAPIKey(ctx, config["api_key"]); err != nil {
		return nil, err
	}
	return config, nil
}

// sourceConfigFor returns the configuration of the gRPC source that listens on
// path, and whether there is one.
func sourceConfigFor(sources []storage.Source, path string) (map[string]string, bool) {
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
			return src.Config, true
		}
	}
	return nil, false
}

// checkAPIKey holds a caller to a source's API key, sent as "x-api-key"
// metadata. A source with no key takes any caller.
func checkAPIKey(ctx context.Context, apiKey string) error {
	if apiKey == "" {
		return nil
	}
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return errors.New("missing metadata")
	}
	tokens := md.Get("x-api-key")
	if len(tokens) == 0 || tokens[0] != apiKey {
		return errors.New("invalid api key")
	}
	return nil
}

// enqueue turns a request into a message and dispatches it to the source that
// holds path. It returns the record's id — also when it fails, so that a stream
// can say which record it could not queue — and, for a source that responds
// synchronously, the waiter for the workflow's result, which the caller must
// Cancel when it is done with it.
func (s *Server) enqueue(path string, req *proto.PublishRequest, config map[string]string) (id string, _ *reply.Pending, _ time.Duration, _ error) {
	// An empty ID reaches SQL sinks as an empty primary key, where every
	// anonymous record upserts the same row. The id is kept here as well as on
	// the message: once dispatched, the message is the engine's and may be back
	// in the pool before this function reads it again.
	id = req.Id
	if id == "" {
		id = uuid.NewString()
	}
	msg := message.AcquireMessage()
	msg.SetID(id)
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
		// The reply id names the caller waiting for a message. It is set
		// below, by this server, and never taken from a request.
		if k == reply.MetaReplyID {
			continue
		}
		msg.SetMetadata(k, v)
	}

	// A source set to respond synchronously holds the call until the workflow
	// has finished with the record. The waiter is registered before the
	// dispatch, or a fast workflow could finish first and answer nobody.
	wait, timeout := reply.ModeOf(config)
	var pending *reply.Pending
	if wait {
		p, err := reply.Expect(msg)
		if err != nil {
			message.ReleaseMessage(msg)
			return id, nil, 0, err
		}
		pending = p
	}

	if err := Dispatch(path, msg); err != nil {
		message.ReleaseMessage(msg)
		if pending != nil {
			pending.Cancel()
		}
		return id, nil, 0, err
	}
	return id, pending, timeout, nil
}

// await waits for the workflow to finish with a record and returns what the
// caller is told.
func await(ctx context.Context, id string, pending *reply.Pending, timeout time.Duration) *proto.PublishResponse {

	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	outcome, err := pending.Wait(waitCtx)
	if err != nil {
		// The wait ran out. The record is still the workflow's and may yet be
		// delivered, so this is not an error: a producer that retries an error
		// sends the record twice.
		return &proto.PublishResponse{Id: id, Status: "pending"}
	}
	return &proto.PublishResponse{
		Id:     id,
		Status: string(outcome.Status),
		Error:  outcome.Error,
		Record: outcome.Record,
	}
}
