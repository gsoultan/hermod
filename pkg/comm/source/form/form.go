package form

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
	sourcebuf "github.com/gsoultan/hermod/pkg/comm/source"
)

// Simple in-memory registry for form sources (for low-latency dispatch).
var (
	registry = make(map[string]chan hermod.Message)
	mu       sync.RWMutex
)

// Register creates a new channel for a form path, superseding any existing
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
		return fmt.Errorf("no form registered for path: %s", path)
	}
	select {
	case ch <- msg:
		return nil
	default:
		return fmt.Errorf("form buffer full for path: %s", path)
	}
}

// FormSource implements the hermod.Source interface for receiving form submissions.
type FormSource struct {
	Path    string
	Storage Storage
	ch      chan hermod.Message
}

// NewFormSource creates a new FormSource.
func NewFormSource(path string, storage Storage) *FormSource {
	if path == "" {
		path = "/api/forms/default"
	}
	return &FormSource{
		Path:    path,
		Storage: storage,
		ch:      Register(path),
	}
}

func (s *FormSource) Read(ctx context.Context) (hermod.Message, error) {
	// 1. Try to read from the in-memory channel (low latency)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case msg, ok := <-s.ch:
		if !ok {
			return nil, errors.New("form source closed")
		}
		// If we got it from the channel, it's already "dispatched" but we should still check if it's in DB
		// Actually, if we use the channel, we should still mark it as processing/completed in DB later.
		// For simplicity, let's assume if it's in the channel, it was just saved to DB as 'pending'.
		return msg, nil
	default:
		// Fall through to polling
	}

	// 2. Poll the database for pending submissions
	if s.Storage == nil {
		// If no storage, just block on the channel
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case msg, ok := <-s.ch:
			if !ok {
				return nil, errors.New("form source closed")
			}
			return msg, nil
		}
	}

	for {
		subs, _, err := s.Storage.ListFormSubmissions(ctx, FormSubmissionFilter{
			Path:   s.Path,
			Status: "pending",
			Limit:  1,
			Page:   1,
		})

		if err == nil && len(subs) > 0 {
			sub := subs[0]
			// Mark as processing to avoid multiple workers picking it up
			_ = s.Storage.UpdateFormSubmissionStatus(ctx, sub.ID, "processing")

			msg := message.AcquireMessage()
			msg.SetID(sub.ID)
			msg.SetOperation(hermod.OpCreate)
			msg.SetTable("form")
			msg.SetAfter(sub.Data)
			msg.SetMetadata("form_path", s.Path)
			return msg, nil
		}

		// Wait before polling again or wait for signal
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case msg, ok := <-s.ch:
			if !ok {
				return nil, errors.New("form source closed")
			}
			return msg, nil
		case <-time.After(5 * time.Second):
			// Continue polling
		}
	}
}

func (s *FormSource) Ack(ctx context.Context, msg hermod.Message) error {
	// A nil message must surface as an error rather than a dereference. The
	// engine acknowledges on the hot path, so a crash — or, as this class of
	// bug has actually behaved elsewhere, a goroutine that never returns —
	// takes the source with it.
	if msg == nil {
		return errors.New("form source: nil message")
	}

	if s.Storage != nil {
		return s.Storage.UpdateFormSubmissionStatus(ctx, msg.ID(), "completed")
	}
	return nil
}

func (s *FormSource) Sample(ctx context.Context, table string) (hermod.Message, error) {
	if s.Storage == nil {
		return nil, errors.New("form source storage not initialized")
	}
	subs, _, err := s.Storage.ListFormSubmissions(ctx, FormSubmissionFilter{
		Path:  s.Path,
		Limit: 1,
		Page:  1,
	})
	if err != nil {
		return nil, err
	}
	if len(subs) == 0 {
		return nil, fmt.Errorf("no submissions found for path %s", s.Path)
	}

	sub := subs[0]
	msg := message.AcquireMessage()
	msg.SetID(sub.ID)
	msg.SetOperation(hermod.OpSnapshot)
	msg.SetTable("form")
	msg.SetAfter(sub.Data)
	msg.SetMetadata("form_path", s.Path)
	return msg, nil
}

func (s *FormSource) Ping(ctx context.Context) error { return nil }

func (s *FormSource) Close() error {
	Unregister(s.Path, s.ch)
	return nil
}
