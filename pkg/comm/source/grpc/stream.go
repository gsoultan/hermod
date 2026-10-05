package grpcsource

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"google.golang.org/grpc"

	"github.com/gsoultan/hermod/pkg/comm/source/grpc/proto"
)

// streamGrantTTL is how long a stream goes on trusting a key check it has
// already made for a path. Checking reads the stored sources, which is too
// much to pay for every record on a stream and is the same answer nearly
// every time. It is not kept for the life of the stream: a key that is
// changed or removed stops working on open streams within this long.
//
// A variable so a test can shorten it.
var streamGrantTTL = 30 * time.Second

const (
	// maxStreamGrants bounds the paths one stream remembers a check for. The
	// path is the caller's to choose, so an unbounded map is the caller's to
	// grow. Past the bound a path is checked every time, as on a single call.
	maxStreamGrants = 32
)

// streamGrant is a key check a stream has already passed for one path.
type streamGrant struct {
	config map[string]string
	at     time.Time
}

// PublishStream is Publish over one long-lived stream. Every record received is
// answered with one response under that record's id: "dispatched", or for a
// source that responds synchronously, what the workflow did with it.
//
// A record that cannot be queued — nothing holds its path, or the source's
// buffer is full — is answered "rejected" and the stream stays open: neither is
// a reason to make a producer reconnect. A record its key does not cover ends
// the stream with the error, as it would end a call.
func (s *Server) PublishStream(stream grpc.BidiStreamingServer[proto.PublishRequest, proto.PublishResponse]) error {
	ctx, cancel := context.WithCancel(stream.Context())
	defer cancel()

	session := &publishSession{
		server: s,
		ctx:    ctx,
		stream: stream,
		grants: make(map[string]streamGrant),
	}

	for {
		req, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			// The producer has finished sending. What it already sent is
			// still answered: each answer comes within the source's response
			// timeout, so this wait is bounded.
			session.answers.Wait()
			return nil
		}
		if err == nil {
			err = session.accept(req)
		}
		if err != nil {
			// Nobody is left to answer. Stop the waits, and let them finish
			// before returning: the stream is over the moment this does, and
			// a send after that is a send on a finished stream.
			cancel()
			session.answers.Wait()
			return err
		}
	}
}

// publishSession is one open PublishStream.
type publishSession struct {
	server *Server
	ctx    context.Context
	stream grpc.BidiStreamingServer[proto.PublishRequest, proto.PublishResponse]

	// sendMu serialises sends. A stream takes one sender at a time, and a
	// synchronous source's answers are sent by one goroutine per record.
	sendMu sync.Mutex
	// answers are the goroutines still waiting to answer a record.
	answers sync.WaitGroup
	// grants are the key checks this stream has already passed, by path. Only
	// the receive loop touches it.
	grants map[string]streamGrant
}

// accept queues one record and answers it, at once or when its workflow has
// finished. It returns an error only for what ends the stream: a record the
// source's key does not cover.
func (p *publishSession) accept(req *proto.PublishRequest) error {
	path := req.Path
	if path == "" {
		path = defaultPath
	}
	config, err := p.configFor(path)
	if err != nil {
		return err
	}

	id, pending, timeout, err := p.server.enqueue(path, req, config)
	if err != nil {
		p.send(&proto.PublishResponse{Id: id, Status: "rejected", Error: err.Error()})
		return nil //nolint:nilerr // the record is answered rejected; the stream is not ended for it
	}
	if pending == nil {
		p.send(&proto.PublishResponse{Id: id, Status: "dispatched"})
		return nil
	}
	p.answers.Go(func() {
		defer pending.Cancel()
		resp := await(p.ctx, id, pending, timeout)
		if p.ctx.Err() != nil {
			return // the stream has gone; there is nobody to tell
		}
		p.send(resp)
	})
	return nil
}

// configFor checks the stream's caller against the source on path and returns
// that source's configuration, trusting a check it made less than
// streamGrantTTL ago.
func (p *publishSession) configFor(path string) (map[string]string, error) {
	grant, seen := p.grants[path]
	if seen && time.Since(grant.at) <= streamGrantTTL {
		return grant.config, nil
	}
	config, err := p.server.authorize(p.ctx, path)
	if err != nil {
		return nil, err
	}
	if seen || len(p.grants) < maxStreamGrants {
		p.grants[path] = streamGrant{config: config, at: time.Now()}
	}
	return config, nil
}

// send writes one answer. A failed send means the stream is broken; the next
// Recv says so.
func (p *publishSession) send(resp *proto.PublishResponse) {
	p.sendMu.Lock()
	defer p.sendMu.Unlock()
	_ = p.stream.Send(resp)
}
