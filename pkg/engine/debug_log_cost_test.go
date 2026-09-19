package engine

// Moving the per-write line to DEBUG (TestASuccessfulWriteDoesNotLogPerMessageAtInfo)
// stopped the log volume but not the cost of producing it. Go evaluates a
// call's arguments whatever the logger then does with them, and one of these
// arguments is the payload's length — which, for a message carrying a data map
// rather than raw bytes, means marshalling the whole map to JSON. Per message.
// For a line the default level (Info) discards.
//
// Measured on BenchmarkEngineThroughput once PayloadLen had stopped copying:
// 954 MB of the remaining 2.24 GB, 43% of everything the engine allocated,
// produced and thrown away.

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// discardSink accepts everything and records nothing.
type discardSink struct{}

func (discardSink) Write(context.Context, hermod.Message) error { return nil }
func (discardSink) Close() error                                { return nil }
func (discardSink) Ping(context.Context) error                  { return nil }

// levelAwareLogger reports whether Debug output is wanted, the way the real
// zerolog-backed logger does.
type levelAwareLogger struct {
	debugOn bool
	debugs  atomic.Int64
}

func (l *levelAwareLogger) Debug(string, ...any) { l.debugs.Add(1) }
func (l *levelAwareLogger) Info(string, ...any)  {}
func (l *levelAwareLogger) Warn(string, ...any)  {}
func (l *levelAwareLogger) Error(string, ...any) {}
func (l *levelAwareLogger) DebugEnabled() bool   { return l.debugOn }

// payloadCountingMessage reports how often anything asked for its payload or
// for the payload's size — the two operations that force the marshal.
type payloadCountingMessage struct {
	*message.DefaultMessage
	reads atomic.Int64
}

func (m *payloadCountingMessage) Payload() []byte {
	m.reads.Add(1)
	return m.DefaultMessage.Payload()
}

func (m *payloadCountingMessage) PayloadLen() int {
	m.reads.Add(1)
	return m.DefaultMessage.PayloadLen()
}

func TestSuccessfulWriteDoesNotMeasurePayloadWhenDebugIsOff(t *testing.T) {
	for _, tc := range []struct {
		name      string
		debugOn   bool
		wantReads int64
	}{
		{"debug disabled", false, 0},
		{"debug enabled", true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := NewEngine(nil, nil, nil)
			lg := &levelAwareLogger{debugOn: tc.debugOn}
			e.logger = lg
			e.workflowID = "wf"
			e.sinkConfigs = []SinkConfig{{}}

			dm := message.AcquireMessage()
			defer dm.Release()
			dm.SetData("id", 1)
			dm.SetData("name", "row")
			msg := &payloadCountingMessage{DefaultMessage: dm}

			if err := e.writeToSink(t.Context(), discardSink{}, msg, "s1", 0); err != nil {
				t.Fatalf("writeToSink: %v", err)
			}

			if got := msg.reads.Load(); got != tc.wantReads {
				t.Errorf("payload measured %d times with debug=%v, want %d: a discarded log line must not marshal the message",
					got, tc.debugOn, tc.wantReads)
			}
			if tc.debugOn && lg.debugs.Load() == 0 {
				t.Error("debug logging is on but the successful write logged nothing")
			}
			if !tc.debugOn && lg.debugs.Load() != 0 {
				t.Errorf("debug logging is off but Debug was called %d times", lg.debugs.Load())
			}
		})
	}
}

// A logger that cannot report its level must keep logging: silently dropping a
// line because we could not ask is a worse failure than the cost this avoids.
func TestWriteStillLogsWhenLoggerCannotReportItsLevel(t *testing.T) {
	e := NewEngine(nil, nil, nil)
	lg := &plainLogger{}
	e.logger = lg
	e.workflowID = "wf"
	e.sinkConfigs = []SinkConfig{{}}

	dm := message.AcquireMessage()
	defer dm.Release()
	dm.SetPayload([]byte(`{"v":1}`))

	if err := e.writeToSink(t.Context(), discardSink{}, dm, "s1", 0); err != nil {
		t.Fatalf("writeToSink: %v", err)
	}
	if lg.debugs.Load() == 0 {
		t.Error("a logger with no level to report got no Debug line for a successful write")
	}
}

type plainLogger struct{ debugs atomic.Int64 }

func (l *plainLogger) Debug(string, ...any) { l.debugs.Add(1) }
func (l *plainLogger) Info(string, ...any)  {}
func (l *plainLogger) Warn(string, ...any)  {}
func (l *plainLogger) Error(string, ...any) {}

// The per-write span's attributes were built eagerly and handed to Start, so
// they were constructed whether or not anything was recording. With no
// TracerProvider installed — the default — that is three attribute values and
// a slice per message, produced for a span that goes nowhere.
//
// Moving them to SetAttributes under IsRecording is only equivalent because
// the sampler does not consult attributes: internal/observability builds the
// provider with WithBatcher and WithResource only, so it gets the default
// ParentBased(AlwaysSample). Installing an attribute-consulting sampler would
// make this a behaviour change; this test is what would notice.
func TestSinkWriteSpanStillCarriesItsAttributes(t *testing.T) {
	rec := recordSpans(t)

	e := NewEngine(nil, nil, nil)
	e.logger = &plainLogger{}
	e.workflowID = "wf-span"
	e.sinkConfigs = []SinkConfig{{}}

	dm := message.AcquireMessage()
	defer dm.Release()
	dm.SetID("msg-1")
	dm.SetPayload([]byte(`{"v":1}`))

	if err := e.writeToSink(t.Context(), discardSink{}, dm, "sink-a", 0); err != nil {
		t.Fatalf("writeToSink: %v", err)
	}

	var found bool
	for _, s := range rec.Ended() {
		if s.Name() != "sink.write" {
			continue
		}
		found = true
		got := map[string]string{}
		for _, a := range s.Attributes() {
			got[string(a.Key)] = a.Value.AsString()
		}
		for k, want := range map[string]string{
			"workflow_id": "wf-span",
			"sink_id":     "sink-a",
			"message_id":  "msg-1",
		} {
			if got[k] != want {
				t.Errorf("span attribute %s = %q, want %q (all: %v)", k, got[k], want, got)
			}
		}
	}
	if !found {
		t.Fatal("no sink.write span was recorded")
	}
}
