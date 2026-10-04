package source

import (
	"errors"
	"slices"
	"sync"

	"github.com/gsoultan/hermod"
)

// ErrPathNotRegistered and ErrPathBufferFull are what Dispatch reports. Each
// push source words its own message around them, because those messages are
// what its callers already read.
var (
	ErrPathNotRegistered = errors.New("no source registered for path")
	ErrPathBufferFull    = errors.New("source buffer full for path")
)

// PathRegistry hands pushed messages to the source that holds a path. The
// gRPC, webhook, GraphQL and form sources each keep one: a request arrives for
// a path, and the source registered under it receives.
//
// A path has one holder. A source built while the path is free holds it at
// once. A source built while another holds it waits, and takes it over on its
// first Read — or when the holder closes, whichever comes first.
//
// The wait is what lets a source be built without being run. Testing a
// connection, sampling and the worker's health check all build a source from
// its configuration, ping it and close it. When the newest registration simply
// owned the path, that probe took it from the workflow that was receiving, and
// closing the probe then deleted it: the workflow reported itself running and
// every request was refused as unregistered until it was restarted. A probe is
// never read, so it never takes over.
//
// Taking over on Read keeps what replacing-on-registration was for: when a
// workflow moves between engines, the one that starts reading is the one that
// receives, and the outgoing one's teardown cannot remove its successor.
type PathRegistry struct {
	mu     sync.RWMutex
	buffer int
	paths  map[string]*pathHolders
}

type pathHolders struct {
	// current receives what is dispatched to the path.
	current chan hermod.Message
	// waiting were registered while another held the path, oldest first.
	waiting []chan hermod.Message
}

// NewPathRegistry returns a registry whose channels buffer the given number of
// messages.
func NewPathRegistry(buffer int) *PathRegistry {
	return &PathRegistry{buffer: buffer, paths: make(map[string]*pathHolders)}
}

// Register creates the channel a source reads its path from. It holds the path
// if nothing else does, and waits otherwise.
func (r *PathRegistry) Register(path string) chan hermod.Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	ch := make(chan hermod.Message, r.buffer)
	holders, ok := r.paths[path]
	if !ok {
		r.paths[path] = &pathHolders{current: ch}
		return ch
	}
	holders.waiting = append(holders.waiting, ch)
	return ch
}

// TakeOver makes a waiting channel the path's holder. A source calls it when it
// starts reading. The channel it replaces is left alone: its reader is on its
// way out, and its own Unregister finds it no longer holds anything.
//
// It does nothing for a channel that already holds the path, or that was never
// registered for it.
func (r *PathRegistry) TakeOver(path string, ch chan hermod.Message) {
	r.mu.Lock()
	defer r.mu.Unlock()
	holders, ok := r.paths[path]
	if !ok {
		return
	}
	i := slices.Index(holders.waiting, ch)
	if i < 0 {
		return
	}
	holders.waiting = slices.Delete(holders.waiting, i, i+1)
	holders.current = ch
}

// Unregister releases a channel's claim on a path.
//
// The holder's channel is closed and the path passes to the newest source
// waiting for it, or is removed if none is. A waiting channel is only taken
// off the list. A channel that has been replaced holds nothing, so releasing
// it changes nothing — which is what stops an outgoing engine's teardown from
// removing the one that took over from it.
func (r *PathRegistry) Unregister(path string, ch chan hermod.Message) {
	r.mu.Lock()
	defer r.mu.Unlock()
	holders, ok := r.paths[path]
	if !ok {
		return
	}
	if holders.current != ch {
		if i := slices.Index(holders.waiting, ch); i >= 0 {
			holders.waiting = slices.Delete(holders.waiting, i, i+1)
		}
		return
	}
	close(ch)
	if n := len(holders.waiting); n > 0 {
		holders.current = holders.waiting[n-1]
		holders.waiting = holders.waiting[:n-1]
		return
	}
	delete(r.paths, path)
}

// Dispatch hands a message to the path's holder without blocking.
//
// The lock is held across the send. Released first, an Unregister could close
// the channel between the lookup and the send, and a send on a closed channel
// panics.
func (r *PathRegistry) Dispatch(path string, msg hermod.Message) error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	holders, ok := r.paths[path]
	if !ok {
		return ErrPathNotRegistered
	}
	select {
	case holders.current <- msg:
		return nil
	default:
		return ErrPathBufferFull
	}
}
