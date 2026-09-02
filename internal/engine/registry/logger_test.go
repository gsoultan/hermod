package registry

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/Hermod/internal/storage"
)

// captureLogger records what a process-level logger was asked to emit.
type captureLogger struct {
	mu    sync.Mutex
	lines []string
}

func (l *captureLogger) record(level, msg string, kv ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s", level, msg)
	for _, v := range kv {
		fmt.Fprintf(&b, " %v", v)
	}
	l.lines = append(l.lines, b.String())
}

func (l *captureLogger) Debug(msg string, kv ...any) { l.record("DEBUG", msg, kv...) }
func (l *captureLogger) Info(msg string, kv ...any)  { l.record("INFO", msg, kv...) }
func (l *captureLogger) Warn(msg string, kv ...any)  { l.record("WARN", msg, kv...) }
func (l *captureLogger) Error(msg string, kv ...any) { l.record("ERROR", msg, kv...) }

func (l *captureLogger) contains(substr string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, line := range l.lines {
		if strings.Contains(line, substr) {
			return true
		}
	}
	return false
}

func (l *captureLogger) count(substr string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, line := range l.lines {
		if strings.Contains(line, substr) {
			n++
		}
	}
	return n
}

// discardingLogCreator stands in for a Registry whose logStorage is nil: it
// accepts every batch and writes it nowhere, exactly as Registry.CreateLogs does
// when no log storage is configured (registry_broadcast.go:389).
type discardingLogCreator struct{}

func (discardingLogCreator) CreateLog(context.Context, storage.Log) error    { return nil }
func (discardingLogCreator) CreateLogs(context.Context, []storage.Log) error { return nil }

// failingLogCreator stands in for a log storage that is present but broken.
type failingLogCreator struct{ err error }

func (f failingLogCreator) CreateLog(context.Context, storage.Log) error { return f.err }
func (f failingLogCreator) CreateLogs(context.Context, []storage.Log) error {
	return f.err
}

// A pipeline that stalls is only recoverable if someone can see that it stalled.
//
// In platform-worker mode the registry is given an API-backed store via
// SetStorage (cmd/hermod/worker_util.go:59) and SetLogStorage is never called,
// so r.logStorage stays nil. setupWorkflowCallbacks nonetheless installs a
// DatabaseLogger over the engine's stderr logger because it guards on r.storage,
// not r.logStorage (registry_workflow.go:620). Registry.CreateLogs then returns
// nil without writing anything, and Flush discards even that.
//
// The result: every engine log line in the worker — including "Pipeline stalled"
// and "Messages acknowledged but delivered nowhere" — went to neither the
// process log nor the database. The signal the supervisor exists to raise was
// being written to a void.
func TestDatabaseLoggerAlwaysReachesTheProcessLog(t *testing.T) {
	t.Run("errors reach the process log when log storage discards them", func(t *testing.T) {
		fallback := &captureLogger{}
		l := NewDatabaseLogger(context.Background(), discardingLogCreator{}, "wf-1", fallback)
		defer l.Close()

		l.Error("Pipeline stalled: work is outstanding but nothing has completed",
			"workflow_id", "wf-1", "stalled_for", "63s")

		if !fallback.contains("Pipeline stalled") {
			t.Fatalf("stall report never reached the process log; lines=%v", fallback.lines)
		}
		if !fallback.contains("wf-1") {
			t.Errorf("structured fields were dropped on the way to the process log; lines=%v", fallback.lines)
		}
	})

	t.Run("every level is teed, not just errors", func(t *testing.T) {
		fallback := &captureLogger{}
		l := NewDatabaseLogger(context.Background(), discardingLogCreator{}, "wf-1", fallback)
		defer l.Close()

		l.Debug("d")
		l.Info("i")
		l.Warn("w")
		l.Error("e")

		for _, want := range []string{"DEBUG d", "INFO i", "WARN w", "ERROR e"} {
			if !fallback.contains(want) {
				t.Errorf("level not teed to the process log: %q; lines=%v", want, fallback.lines)
			}
		}
	})

	t.Run("a failing database write is reported instead of swallowed", func(t *testing.T) {
		fallback := &captureLogger{}
		l := NewDatabaseLogger(context.Background(), failingLogCreator{err: errors.New("connection refused")}, "wf-1", fallback)
		defer l.Close()

		l.Error("something broke")
		l.Flush()

		if !fallback.contains("connection refused") {
			t.Fatalf("the database log write failed silently; lines=%v", fallback.lines)
		}
	})

	t.Run("repeated database failures are throttled, not flooded", func(t *testing.T) {
		fallback := &captureLogger{}
		l := NewDatabaseLogger(context.Background(), failingLogCreator{err: errors.New("connection refused")}, "wf-1", fallback)
		defer l.Close()

		for range 20 {
			l.Error("something broke")
			l.Flush()
		}

		if n := fallback.count("connection refused"); n > 1 {
			t.Errorf("flush failure reported %d times; a broken database would flood the process log", n)
		}
	})

	t.Run("a nil fallback logger is safe", func(t *testing.T) {
		l := NewDatabaseLogger(context.Background(), discardingLogCreator{}, "wf-1", nil)
		defer l.Close()
		l.Error("must not panic")
	})

	t.Run("Close stops the background flusher", func(t *testing.T) {
		fallback := &captureLogger{}
		l := NewDatabaseLogger(context.Background(), discardingLogCreator{}, "wf-1", fallback)
		l.Close()
		// Closing twice must not panic on a double cancel or double close.
		l.Close()
		// Give the goroutine a moment to unwind so -race can observe a leak.
		time.Sleep(10 * time.Millisecond)
	})
}
