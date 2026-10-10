package engine

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/buffer"
	"github.com/gsoultan/hermod/pkg/engine/config"
	"github.com/gsoultan/hermod/pkg/engine/telemetry"
)

// The read loop published source status "running" before every read, and each
// publication builds a full StatusUpdate — every node, error, latency and edge
// map — and hands it to the registry, which applies and broadcasts it. That
// is one status notification per message. Status is pushed on change and on
// the health tick; a steady stream of messages is not a change.
func TestSteadyMessagesDoNotNotifyStatusPerMessage(t *testing.T) {
	const messages = 5000

	src := newBenchSource(messages, 64)
	snk := newNullSink(messages)
	eng := NewEngine(src, []hermod.Sink{snk}, buffer.NewRingBuffer(4096))
	eng.SetConfig(config.DefaultConfig())
	eng.SetSinkConfigs([]config.SinkConfig{{BackpressureBuffer: 4096}})
	eng.SetLogger(benchLogger{})

	var notifications atomic.Int64
	eng.SetOnStatusChange(func(telemetry.StatusUpdate) { notifications.Add(1) })

	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	go func() { _ = eng.Start(ctx) }()

	select {
	case <-snk.done:
	case <-ctx.Done():
		t.Fatalf("timed out before draining %d messages", messages)
	}
	cancel()

	// Startup and a health tick or two publish a handful. Per message would
	// be thousands.
	if n := notifications.Load(); n > 100 {
		t.Fatalf("%d status notifications for %d messages; status must not be published per message", n, messages)
	}
}
