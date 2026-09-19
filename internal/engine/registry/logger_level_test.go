package registry

// DatabaseLogger had no level filter at all. Every Debug line was built and
// buffered for the log table, so moving the per-write success line from Info to
// Debug silenced the process logger and changed nothing here: a healthy
// pipeline still persisted one database row per message.
//
// It also meant the engine's debugEnabled() guard — which exists to avoid
// marshalling a message just to report its size — defaulted to "yes, log",
// because a logger that cannot report a level is assumed to want the line. So
// every message paid a full JSON marshal of its data map for a row nobody
// reads.
//
// Measured on BenchmarkWorkflowThroughput at 32 columns: the entry building was
// 295k allocations and the marshal it failed to prevent another 754k, together
// about 30% of everything the workflow allocated.

import (
	"context"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
)

// countingLogStore records what the logger tried to persist.
type countingLogStore struct{ created int }

func (s *countingLogStore) CreateLog(ctx context.Context, l storage.Log) error {
	s.created++
	return nil
}

func (s *countingLogStore) CreateLogs(ctx context.Context, logs []storage.Log) error {
	s.created += len(logs)
	return nil
}

func newLevelTestLogger(t *testing.T) *DatabaseLogger {
	t.Helper()
	lg := NewDatabaseLogger(context.Background(), &countingLogStore{}, "wf", noopLevelLogger{})
	t.Cleanup(lg.Close)
	return lg
}

type noopLevelLogger struct{}

func (noopLevelLogger) Debug(string, ...any) {}
func (noopLevelLogger) Info(string, ...any)  {}
func (noopLevelLogger) Warn(string, ...any)  {}
func (noopLevelLogger) Error(string, ...any) {}

func TestDatabaseLoggerDropsDebugByDefault(t *testing.T) {
	lg := newLevelTestLogger(t)

	lg.Debug("a debug line", "k", "v")
	if n := lg.buffered(); n != 0 {
		t.Errorf("Debug buffered %d entries at the default level; a healthy pipeline would persist one row per message", n)
	}

	// Everything at Info and above is still kept.
	lg.Info("an info line")
	lg.Warn("a warning")
	lg.Error("an error")
	if n := lg.buffered(); n != 3 {
		t.Errorf("buffered %d entries after Info+Warn+Error, want 3", n)
	}
}

func TestDatabaseLoggerKeepsDebugWhenAskedTo(t *testing.T) {
	t.Setenv("HERMOD_LOG_LEVEL", "debug")
	lg := newLevelTestLogger(t)

	lg.Debug("a debug line", "k", "v")
	if n := lg.buffered(); n != 1 {
		t.Errorf("buffered %d entries with HERMOD_LOG_LEVEL=debug, want 1: turning debug on must still work", n)
	}
}

// DebugEnabled is what lets a caller skip building an argument the level is
// about to discard. Without it the engine assumes the line is wanted.
func TestDatabaseLoggerReportsItsDebugLevel(t *testing.T) {
	if lg := newLevelTestLogger(t); lg.DebugEnabled() {
		t.Error("DebugEnabled() is true at the default level")
	}

	t.Setenv("HERMOD_LOG_LEVEL", "debug")
	if lg := newLevelTestLogger(t); !lg.DebugEnabled() {
		t.Error("DebugEnabled() is false with HERMOD_LOG_LEVEL=debug")
	}
}

// A dropped Debug line must not be built at all — the point is to avoid the
// work, not to throw the result away afterwards.
func TestDroppedDebugLineIsNotBuilt(t *testing.T) {
	lg := newLevelTestLogger(t)

	var built int
	stringer := funcStringer(func() string { built++; return "expensive" })

	lg.Debug("a debug line", "value", stringer)
	if built != 0 {
		t.Errorf("a dropped Debug line still formatted its arguments %d time(s)", built)
	}

	lg.Error("an error line", "value", stringer)
	if built == 0 {
		t.Error("a kept Error line never formatted its arguments")
	}
}

type funcStringer func() string

func (f funcStringer) String() string { return f() }
