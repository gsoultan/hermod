// Package reply carries what a workflow did with a message back to a caller
// that is waiting for it.
//
// A push source answers its caller as soon as the message is queued, so the
// caller never learns whether the workflow delivered it. A transport that wants
// to answer with the result instead calls Expect before it dispatches the
// message and waits; the engine, when it has finished with the message, calls
// Resolve. The two meet through an id carried in the message's metadata, so
// nothing between them — source wrappers, buffers, the router — has to know.
//
// Both ends are in one process: a push source's transport and the engine that
// runs its workflow already have to be.
package reply

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gsoultan/hermod"
)

// MetaReplyID is the metadata key under which an awaited message carries the
// id its waiter is registered under.
const MetaReplyID = "_hermod_reply_id"

// Status says what the workflow did with a message.
type Status string

const (
	// Delivered: written to every sink the workflow routed it to.
	Delivered Status = "delivered"
	// Completed: the workflow ran and had nothing to write — it has no sink, or
	// it is a dry run.
	Completed Status = "completed"
	// Filtered: the workflow ran and chose to deliver it nowhere — a filter
	// dropped it, or its outcome had no edge to follow.
	Filtered Status = "filtered"
	// DeadLettered: it failed and was parked in the dead-letter sink.
	DeadLettered Status = "dead_lettered"
	// Failed: it failed and is not preserved anywhere.
	Failed Status = "failed"
)

// Outcome is what the waiting caller is told.
type Outcome struct {
	Status Status
	// Error describes the failure. Empty for Delivered and Completed.
	Error string
	// Record is the message as the workflow left it, as JSON: what a sink was
	// sent when it was delivered, and the message itself otherwise.
	Record []byte
}

// ErrTooManyPending is returned by Expect when the limit on callers waiting at
// once is reached.
var ErrTooManyPending = errors.New("too many requests are waiting for a workflow result")

// defaultMaxPending bounds the callers held open at once. Each is a request
// that is not answered until its workflow finishes or its timeout passes, so
// without a bound a stalled workflow behind a busy endpoint grows the table for
// as long as requests keep arriving.
const defaultMaxPending = 10_000

var (
	mu         sync.Mutex
	pending    = make(map[string]chan Outcome)
	maxPending = defaultMaxPending
)

// Pending is one caller waiting for one message.
type Pending struct {
	id string
	ch chan Outcome
}

// Expect marks msg as awaited and registers the caller as its waiter. Call it
// before the message is dispatched, and Cancel when done waiting.
func Expect(msg hermod.Message) (*Pending, error) {
	mu.Lock()
	defer mu.Unlock()
	if len(pending) >= maxPending {
		return nil, ErrTooManyPending
	}
	p := &Pending{id: uuid.NewString(), ch: make(chan Outcome, 1)}
	pending[p.id] = p.ch
	msg.SetMetadata(MetaReplyID, p.id)
	return p, nil
}

// Wait blocks until the engine resolves the message or ctx ends.
func (p *Pending) Wait(ctx context.Context) (Outcome, error) {
	select {
	case o := <-p.ch:
		return o, nil
	case <-ctx.Done():
		return Outcome{}, ctx.Err()
	}
}

// Cancel stops waiting. An outcome that arrives afterwards is dropped. It is
// safe to call more than once and after the message was resolved.
func (p *Pending) Cancel() {
	mu.Lock()
	defer mu.Unlock()
	delete(pending, p.id)
}

// IDOf returns the reply id of an awaited message.
func IDOf(msg hermod.Message) (string, bool) {
	id, ok := hermod.MetadataValue(msg, MetaReplyID)
	return id, ok && id != ""
}

// Take returns the reply id of an awaited message and removes it from the
// message. The engine calls it as it starts on a message: the id is how it
// finds the caller, and from there on it is nobody else's business — left on
// the message it is written to every sink and handed back in the record.
func Take(msg hermod.Message) (string, bool) {
	id, ok := IDOf(msg)
	if !ok {
		return "", false
	}
	if d, can := msg.(interface{ DeleteMetadata(key string) }); can {
		d.DeleteMetadata(MetaReplyID)
	} else {
		// A message that cannot drop a key carries it empty, which IDOf
		// reads as not awaited.
		msg.SetMetadata(MetaReplyID, "")
	}
	return id, true
}

// Resolve hands an outcome to the caller waiting under id, and reports whether
// there was one. A message is resolved once: the waiter is removed as it is
// answered, so a replayed message carrying the same id reaches nobody.
func Resolve(id string, o Outcome) bool {
	mu.Lock()
	ch, ok := pending[id]
	if ok {
		delete(pending, id)
	}
	mu.Unlock()
	if !ok {
		return false
	}
	// The channel has room for one and this is the only send it ever gets.
	ch <- o
	return true
}

func pendingCount() int {
	mu.Lock()
	defer mu.Unlock()
	return len(pending)
}

// setMaxPending is for tests.
func setMaxPending(n int) (restore func()) {
	mu.Lock()
	defer mu.Unlock()
	old := maxPending
	maxPending = n
	return func() {
		mu.Lock()
		defer mu.Unlock()
		maxPending = old
	}
}

// Config keys a push source carries to say how it answers its caller.
const (
	ConfigMode    = "response_mode"
	ConfigTimeout = "response_timeout"

	// ModeSync holds the caller until the workflow has finished with the
	// message. Anything else, including nothing, answers once it is queued.
	ModeSync = "sync"
)

const (
	// DefaultTimeout is how long a synchronous caller is held when the source
	// names no timeout, or one that does not parse.
	DefaultTimeout = 30 * time.Second
	// MaxTimeout caps what a source may ask for. A request held open is a
	// connection and a waiter held open.
	MaxTimeout = 5 * time.Minute
)

// ModeOf reads how a source answers from its configuration: whether the caller
// waits, and for how long.
//
// A timeout that is missing, unreadable or not positive is the default, never
// "no timeout" and never a fall back to answering at once.
func ModeOf(config map[string]string) (wait bool, timeout time.Duration) {
	if config[ConfigMode] != ModeSync {
		return false, 0
	}
	timeout = DefaultTimeout
	if d, err := time.ParseDuration(config[ConfigTimeout]); err == nil && d > 0 {
		timeout = min(d, MaxTimeout)
	}
	return true, timeout
}
