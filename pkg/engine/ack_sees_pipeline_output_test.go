package engine

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/buffer"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// recordingAckSource captures the message the engine hands to Ack, so a test
// can ask what state it was in by then.
type recordingAckSource struct {
	msg hermod.Message

	mu        sync.Mutex
	acked     map[string]any
	ackedMeta map[string]string
	ackedC    chan struct{}
	once      sync.Once
}

func (s *recordingAckSource) Read(ctx context.Context) (hermod.Message, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(5 * time.Millisecond):
		if s.msg == nil {
			return nil, nil
		}
		return s.msg.Clone(), nil
	}
}

func (s *recordingAckSource) Ack(_ context.Context, msg hermod.Message) error {
	s.mu.Lock()
	if s.acked == nil {
		s.acked = map[string]any{}
	}
	for k, v := range msg.Data() {
		s.acked[k] = v
	}
	s.ackedMeta = map[string]string{}
	for k, v := range msg.Metadata() {
		s.ackedMeta[k] = v
	}
	s.mu.Unlock()
	s.once.Do(func() { close(s.ackedC) })
	return nil
}

func (s *recordingAckSource) Ping(context.Context) error { return nil }
func (s *recordingAckSource) Close() error               { return nil }

// TestAckReceivesThePipelinesOutputNotTheSourcesInput pins the contract the
// metis external-task source is built on.
//
// That source completes a BPMN task in its Ack, and what it sends as the
// process's output variables is whatever the acknowledged message carries. That
// is only the right answer if the engine acknowledges the message as the
// pipeline left it — if Ack were handed the message as it was read, every task
// would complete with its own input echoed back and the work the pipeline did
// would never reach the process.
//
// The router is what makes it so: for a node-graph workflow it runs the whole
// traversal and rewrites the message in place, so the engine's `m` is the
// output by the time the write succeeds. This test fails if that ever stops
// being true.
func TestAckReceivesThePipelinesOutputNotTheSourcesInput(t *testing.T) {
	msg := message.AcquireMessage()
	msg.SetID("task-1")
	msg.SetData("amount", 500)

	source := &recordingAckSource{msg: msg, ackedC: make(chan struct{})}
	sink := &counterSink{}
	eng := NewEngine(source, []hermod.Sink{sink}, buffer.NewRingBuffer(10))

	// Stand in for a node-graph traversal: rewrite the message, then route it.
	eng.SetRouter(func(_ context.Context, m hermod.Message) ([]RoutedMessage, error) {
		m.SetData("reversed", true)
		m.Retain()
		return []RoutedMessage{{SinkIndex: 0, Message: m}}, nil
	})

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	go func() { _ = eng.Start(ctx) }()

	select {
	case <-source.ackedC:
	case <-ctx.Done():
		t.Fatal("engine never acknowledged a message")
	}

	source.mu.Lock()
	defer source.mu.Unlock()
	if got, ok := source.acked["reversed"]; !ok || got != true {
		t.Fatalf("Ack was handed the source's input, not the pipeline's output: "+
			"want reversed=true in %v", source.acked)
	}
	if got, ok := source.acked["amount"]; !ok || got != 500 {
		t.Fatalf("the pipeline's input should survive into the output: want amount=500 in %v", source.acked)
	}
}

// dlqSink accepts everything, standing in for a reachable dead-letter sink.
type dlqSink struct{}

func (dlqSink) Write(context.Context, hermod.Message) error { return nil }
func (dlqSink) Ping(context.Context) error                  { return nil }
func (dlqSink) Close() error                                { return nil }

// refusingSink is the outage.
type refusingSink struct{}

func (refusingSink) Write(context.Context, hermod.Message) error {
	return errors.New("connection reset by peer")
}
func (refusingSink) Ping(context.Context) error { return nil }
func (refusingSink) Close() error               { return nil }

// TestADeadLetteredMessageIsAcknowledgedCarryingAMarkerSayingSo pins the second
// contract the metis external-task source is built on.
//
// The engine acknowledges a message it could not deliver but did preserve, so
// that a replication slot advances rather than replaying something already
// kept. A source whose Ack means "this step of a business process succeeded"
// cannot take that at face value: it has to tell a delivery apart from a park,
// and the only thing distinguishing them is the metadata the engine sets before
// acknowledging.
//
// If a future change parks a message without marking it, that source completes
// a BPMN task on work that is sitting in a dead-letter queue, and the process
// advances to its next step — approving a payment, shipping an order — on the
// strength of it. This test fails first.
func TestADeadLetteredMessageIsAcknowledgedCarryingAMarkerSayingSo(t *testing.T) {
	// Every key the engine sets to mean "preserved, not delivered". A source
	// deciding whether an acknowledgement was a success reads these.
	// Only _hermod_failed_at is set on every park. _hermod_dead_lettered is set
	// on top of it when a node failed, and a source reading that one alone
	// misses the sink-outage case entirely — which is the common one.
	markers := []string{"_hermod_dead_lettered", "_hermod_validation_failed", "_hermod_failed_at"}

	cases := []struct {
		name  string
		route func(eng *Engine) RouterFunc
	}{
		{
			// A node failed: the traversal parks the message itself and then
			// resolves no sink for it.
			name: "a node failed",
			route: func(eng *Engine) RouterFunc {
				return func(ctx context.Context, msg hermod.Message) ([]RoutedMessage, error) {
					eng.DeadLetterNodeFailure(ctx, "T", msg, errors.New("could not parse the payload"))
					return nil, nil
				}
			},
		},
		{
			// Nothing failed in the traversal; the workflow simply resolved
			// none of its sinks, which the engine parks on the message's behalf.
			name: "no sink resolved",
			route: func(*Engine) RouterFunc {
				return func(context.Context, hermod.Message) ([]RoutedMessage, error) {
					return nil, nil
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := message.AcquireMessage()
			msg.SetID("task-1")
			msg.SetData("amount", 500)

			source := &recordingAckSource{msg: msg, ackedC: make(chan struct{})}
			eng := NewEngine(source, []hermod.Sink{refusingSink{}}, buffer.NewRingBuffer(8))
			eng.SetDeadLetterSink(dlqSink{})
			eng.SetRouter(tc.route(eng))

			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			go func() { _ = eng.Start(ctx) }()

			select {
			case <-source.ackedC:
			case <-ctx.Done():
				t.Fatal("the engine never acknowledged the parked message")
			}

			source.mu.Lock()
			defer source.mu.Unlock()
			for _, marker := range markers {
				if source.ackedMeta[marker] != "" {
					return // the park is legible
				}
			}
			t.Fatalf("the message was parked and acknowledged carrying none of %v; "+
				"a source cannot tell this from a successful delivery. Metadata: %v",
				markers, source.ackedMeta)
		})
	}
}
