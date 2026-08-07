package engine

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/comm/buffer"
	"github.com/user/hermod/pkg/comm/message"
	"github.com/user/hermod/pkg/engine/config"
)

// A pipeline can be wedged upstream of everything the engine counts.
//
// pkg/engine/stall.go documented this limit explicitly: the progress sample only
// sees work the engine has already accepted, so "a source that stops handing
// messages over at all looks identical to a source with nothing to send". For a
// logical replication source that is not true from the source's own point of
// view — PostgreSQL sends a keepalive on an otherwise idle stream every
// wal_sender_timeout/2 (verified on the dev database: wal_sender_timeout is
// 60s, so every ~30s). A stream that has received nothing at all, not even a
// keepalive, well past that cadence is broken, not idle — and that is true
// whether or not the slot happens to be reporting lag yet.
func TestStreamSilenceIsAWedge(t *testing.T) {
	t0 := time.Unix(1_700_000_000, 0)

	t.Run("silence past the threshold is a wedge", func(t *testing.T) {
		wedged, silent := streamSilenceWedge(t0, 90*time.Second, t0.Add(91*time.Second))
		if !wedged {
			t.Fatal("a stream silent past its keepalive cadence was treated as healthy")
		}
		if silent != 91*time.Second {
			t.Errorf("silentFor = %v, want 91s", silent)
		}
	})

	t.Run("silence inside the threshold is not", func(t *testing.T) {
		if wedged, _ := streamSilenceWedge(t0, 90*time.Second, t0.Add(89*time.Second)); wedged {
			t.Error("reported a wedge before a keepalive was even due")
		}
	})

	t.Run("a zero threshold disables the check", func(t *testing.T) {
		// wal_sender_timeout = 0 disables keepalives, so silence proves nothing.
		if wedged, _ := streamSilenceWedge(t0, 0, t0.Add(24*time.Hour)); wedged {
			t.Error("reported a wedge on a server that does not send keepalives at all")
		}
	})

	t.Run("a stream that has not started yet is not a wedge", func(t *testing.T) {
		if wedged, _ := streamSilenceWedge(time.Time{}, 90*time.Second, t0); wedged {
			t.Error("reported a wedge before the stream had produced anything")
		}
	})
}

// silentStreamSource holds its replication stream open and answers every health
// check, while receiving nothing on it — not even a keepalive. Its slot reports
// no lag, so nothing the engine counts is outstanding: this is precisely the
// wedge the progress sample cannot see.
type silentStreamSource struct {
	last      atomic.Int64
	threshold time.Duration
}

func (s *silentStreamSource) Read(ctx context.Context) (hermod.Message, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (s *silentStreamSource) Ack(context.Context, hermod.Message) error { return nil }
func (s *silentStreamSource) Ping(context.Context) error                { return nil }
func (s *silentStreamSource) Close() error                              { return nil }
func (s *silentStreamSource) IsReady(context.Context) error             { return nil }
func (s *silentStreamSource) GetLag(context.Context) (uint64, error)    { return 0, nil }

func (s *silentStreamSource) LastStreamActivity() time.Time {
	return time.Unix(0, s.last.Load())
}

func (s *silentStreamSource) StreamSilenceThreshold() time.Duration { return s.threshold }

// backloggedSource keeps delivering messages while its stream-activity clock
// stays frozen — what a source looks like when its handover to the pipeline is
// blocked behind a consumer that is catching up.
type backloggedSource struct {
	silentStreamSource
	msg hermod.Message
}

func (s *backloggedSource) Read(ctx context.Context) (hermod.Message, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(10 * time.Millisecond):
		return s.msg.Clone(), nil
	}
}

func TestWatchdogReportsASilentReplicationStream(t *testing.T) {
	t.Run("a stream that has gone silent reaches the supervisor", func(t *testing.T) {
		src := &silentStreamSource{threshold: time.Second}
		// The stream was last alive well beyond its keepalive cadence.
		src.last.Store(time.Now().Add(-time.Minute).UnixNano())

		eng := NewEngine(src, []hermod.Sink{&mockSink{}}, buffer.NewRingBuffer(64))
		cfg := config.DefaultConfig()
		cfg.StallThreshold = time.Hour // the progress-based path must not be what fires
		cfg.StreamSilenceInterval = 200 * time.Millisecond
		cfg.StatusInterval = 100 * time.Millisecond
		eng.SetConfig(cfg)
		eng.SetWorkflowID("wf-silent-stream")

		stalled := make(chan string, 1)
		eng.SetOnStall(func(reason string) {
			select {
			case stalled <- reason:
			default:
			}
		})

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		go func() { _ = eng.Start(ctx) }()

		select {
		case reason := <-stalled:
			if reason == "" {
				t.Error("the supervisor was given no reason")
			}
		case <-time.After(15 * time.Second):
			t.Fatal("a replication stream that stopped receiving anything was never reported")
		}
	})

	t.Run("a backlogged pipeline that is still draining is left alone", func(t *testing.T) {
		// The source's handover blocks while the consumer is behind, which
		// blocks its receive loop too — so a slow sink working through a backlog
		// presents as a stream that has stopped receiving. Restarting it would
		// throw away the progress it is making and replay the same backlog.
		msg := message.AcquireMessage()
		msg.SetID("backlog-1")
		msg.SetPayload([]byte("payload"))

		src := &backloggedSource{msg: msg}
		src.threshold = 500 * time.Millisecond
		// Its stream last received something well past the deadline.
		src.last.Store(time.Now().Add(-time.Hour).UnixNano())

		eng := NewEngine(src, []hermod.Sink{&mockSink{}}, buffer.NewRingBuffer(64))
		cfg := config.DefaultConfig()
		cfg.StallThreshold = time.Hour
		cfg.StreamSilenceInterval = 200 * time.Millisecond
		cfg.StatusInterval = 100 * time.Millisecond
		eng.SetConfig(cfg)
		eng.SetWorkflowID("wf-backlogged")

		stalled := make(chan string, 1)
		eng.SetOnStall(func(reason string) {
			select {
			case stalled <- reason:
			default:
			}
		})

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		go func() { _ = eng.Start(ctx) }()

		select {
		case reason := <-stalled:
			t.Fatalf("a pipeline that was still completing messages was restarted for a quiet stream: %s", reason)
		case <-time.After(4 * time.Second):
		}
	})

	t.Run("a live stream is left alone", func(t *testing.T) {
		src := &silentStreamSource{threshold: 2 * time.Second}
		src.last.Store(time.Now().UnixNano())

		eng := NewEngine(src, []hermod.Sink{&mockSink{}}, buffer.NewRingBuffer(64))
		cfg := config.DefaultConfig()
		cfg.StallThreshold = time.Hour
		cfg.StreamSilenceInterval = 100 * time.Millisecond
		cfg.StatusInterval = 100 * time.Millisecond
		eng.SetConfig(cfg)
		eng.SetWorkflowID("wf-live-stream")

		stalled := make(chan string, 1)
		eng.SetOnStall(func(reason string) {
			select {
			case stalled <- reason:
			default:
			}
		})

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		go func() { _ = eng.Start(ctx) }()

		// Keep the stream alive the way a keepalive would.
		done := make(chan struct{})
		defer close(done)
		go func() {
			tick := time.NewTicker(200 * time.Millisecond)
			defer tick.Stop()
			for {
				select {
				case <-done:
					return
				case <-tick.C:
					src.last.Store(time.Now().UnixNano())
				}
			}
		}()

		select {
		case reason := <-stalled:
			t.Fatalf("an idle-but-alive stream was restarted: %s", reason)
		case <-time.After(3 * time.Second):
		}
	})
}
