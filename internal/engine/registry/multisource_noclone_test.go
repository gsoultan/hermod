package registry

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// cloneCountingMessage counts deep copies taken of a message.
type cloneCountingMessage struct {
	hermod.Message
	clones *atomic.Int64
}

func (m *cloneCountingMessage) Clone() hermod.Message {
	m.clones.Add(1)
	return m.Message.Clone()
}

type cloneCountingSource struct {
	clones atomic.Int64
}

func (s *cloneCountingSource) Read(ctx context.Context) (hermod.Message, error) {
	msg := message.AcquireMessage()
	msg.SetData("payload", "x")
	return &cloneCountingMessage{Message: msg, clones: &s.clones}, nil
}
func (s *cloneCountingSource) Ack(context.Context, hermod.Message) error { return nil }
func (s *cloneCountingSource) Ping(context.Context) error                { return nil }
func (s *cloneCountingSource) Close() error                              { return nil }

// The multi-source reader used to deep-clone every message it forwarded into a
// per-source "last delivered" cache that nothing outside a test ever read: a
// full payload copy per message, for nothing. Forwarding a message must not
// copy it.
func TestMultiSourceForwardsMessagesWithoutCloning(t *testing.T) {
	src := &cloneCountingSource{}
	ms := &multiSource{
		msgChan: make(chan hermod.Message, 64),
		errChan: make(chan error, 8),
		sources: []*subSource{{nodeID: "node-src", sourceID: "src-1", source: src}},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	defer func() { _ = ms.Close() }()

	for i := range 50 {
		msg, err := ms.Read(ctx)
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		if msg == nil {
			t.Fatalf("read %d: nil message", i)
		}
	}
	cancel()

	if n := src.clones.Load(); n != 0 {
		t.Fatalf("forwarding 50 messages took %d deep clones; want 0", n)
	}
}
