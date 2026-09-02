package engine

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/buffer"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// kvLogger keeps whole lines — message and key/values — so a test can assert
// on what an operator actually reads, not just the headline.
type kvLogger struct {
	mu    sync.Mutex
	lines []string
}

func (l *kvLogger) log(msg string, kv ...any) {
	l.mu.Lock()
	l.lines = append(l.lines, msg+" | "+fmt.Sprint(kv...))
	l.mu.Unlock()
}
func (l *kvLogger) Debug(msg string, kv ...any) { l.log(msg, kv...) }
func (l *kvLogger) Info(msg string, kv ...any)  { l.log(msg, kv...) }
func (l *kvLogger) Warn(msg string, kv ...any)  { l.log(msg, kv...) }
func (l *kvLogger) Error(msg string, kv ...any) { l.log(msg, kv...) }

func (l *kvLogger) joined() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

// The unroutable report must describe what actually happens to the message,
// which depends on whether a dead-letter sink exists. It used to say "the
// source has been acknowledged" unconditionally — for the no-DLQ branch that
// told the operator their data was gone at the exact moment the engine was
// preserving it by NOT acknowledging.
func TestUnroutableReportMatchesTheActualDisposition(t *testing.T) {
	m := message.AcquireMessage()
	t.Cleanup(m.Release)
	m.SetID("u-1")

	t.Run("without a DLQ it says the message is kept, not acknowledged", func(t *testing.T) {
		log := &kvLogger{}
		eng := NewEngine(&countingSource{}, []hermod.Sink{&countingSink{done: make(chan struct{})}}, buffer.NewRingBuffer(4))
		eng.SetLogger(log)

		eng.reportUnroutable(m)

		out := log.joined()
		if !strings.Contains(out, "NOT acknowledged") || !strings.Contains(out, "redelivered") {
			t.Errorf("the report does not say the message survives:\n%s", out)
		}
		if strings.Contains(out, "has been acknowledged") {
			t.Errorf("the report claims an acknowledgment that never happened:\n%s", out)
		}
	})

	t.Run("with a DLQ it points at the DLQ", func(t *testing.T) {
		log := &kvLogger{}
		eng := NewEngine(&countingSource{}, []hermod.Sink{&countingSink{done: make(chan struct{})}}, buffer.NewRingBuffer(4))
		eng.SetLogger(log)
		eng.SetDeadLetterSink(&countingSink{done: make(chan struct{})})

		eng.reportUnroutable(m)

		out := log.joined()
		if !strings.Contains(out, "dead-letter sink") || !strings.Contains(out, "DLQ") {
			t.Errorf("the report does not point at the DLQ:\n%s", out)
		}
	})
}
