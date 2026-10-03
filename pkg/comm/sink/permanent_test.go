package sink

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
)

// scriptedSink fails every write with err and counts the attempts.
type scriptedSink struct {
	hermod.Sink
	mu    sync.Mutex
	err   error
	calls int
}

func (s *scriptedSink) Write(context.Context, hermod.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.err
}

func (s *scriptedSink) WriteBatch(context.Context, []hermod.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.err
}

func (s *scriptedSink) attempts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// scriptedBatchSink is a scriptedSink that is also a hermod.BatchSink, so
// RetrySink takes its whole-batch retry path.
type scriptedBatchSink struct{ *scriptedSink }

var _ hermod.BatchSink = scriptedBatchSink{}

// An FCM refusal of a 4.6 KB payload was reported as "sink write failed after
// 3 retries: fcm: permanent failure": the sink said the message could never be
// sent, and the decorator sent it twice more anyway. For a dead device token
// that is two more real calls to FCM per message before the dead-letter sink.
func TestRetrySinkStopsOnAPermanentRefusal(t *testing.T) {
	refusal := fmt.Errorf("payload is 4633 bytes, the limit is 4096: %w", hermod.ErrPermanent)

	t.Run("write", func(t *testing.T) {
		s := &scriptedSink{err: refusal}
		err := NewRetrySink(s, 3, time.Millisecond, nil).Write(context.Background(), nil)

		if n := s.attempts(); n != 1 {
			t.Errorf("the sink was asked %d times, want 1: a permanent refusal is the same on every attempt", n)
		}
		if !errors.Is(err, hermod.ErrPermanent) {
			t.Errorf("error %v no longer says it is permanent; the engine needs that to skip its own retries", err)
		}
		if strings.Contains(err.Error(), "retries") {
			t.Errorf("error %q claims retries that did not happen", err)
		}
	})

	t.Run("batch", func(t *testing.T) {
		s := scriptedBatchSink{&scriptedSink{err: refusal}}
		err := NewRetrySink(s, 3, time.Millisecond, nil).WriteBatch(context.Background(), []hermod.Message{nil, nil})

		if n := s.attempts(); n != 1 {
			t.Errorf("the batch was sent %d times, want 1", n)
		}
		if !errors.Is(err, hermod.ErrPermanent) {
			t.Errorf("error %v no longer says it is permanent", err)
		}
	})

	// The control: an ordinary failure still gets the whole budget, so the
	// tests above are not passing because retrying stopped altogether.
	t.Run("a transient failure is still retried", func(t *testing.T) {
		s := &scriptedSink{err: errors.New("connection reset")}
		_ = NewRetrySink(s, 3, time.Millisecond, nil).Write(context.Background(), nil)
		if n := s.attempts(); n != 3 {
			t.Errorf("the sink was asked %d times, want 3", n)
		}
	})
}
