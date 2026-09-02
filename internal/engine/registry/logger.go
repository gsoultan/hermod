package registry

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	hermod "github.com/gsoultan/Hermod"
	"github.com/gsoultan/Hermod/internal/storage"
)

type LogCreator interface {
	CreateLog(ctx context.Context, log storage.Log) error
	CreateLogs(ctx context.Context, logs []storage.Log) error
}

type DatabaseLogger struct {
	storage    LogCreator
	ctx        context.Context
	cancel     context.CancelFunc
	workflowID string

	// fallback is the process logger.
	fallback hermod.Logger

	// flushing guards against piling up flush goroutines when storage is slow.
	flushing atomic.Bool

	mu           sync.Mutex
	buffer       []storage.Log
	sampleRate   float64
	closeOnce    sync.Once
	lastFlushErr time.Time
	// dropped counts entries discarded because storage was not keeping up.
	dropped int
}

const (
	// dbLogBufferFlushAt is the buffered-entry count that triggers a flush.
	dbLogBufferFlushAt = 50
	// dbLogBufferMax bounds the buffer when storage is unreachable. A worker
	// whose platform is down keeps running and keeps logging; without a cap it
	// would accumulate every line until the process died of it.
	dbLogBufferMax = 5000
)

func NewDatabaseLogger(parentCtx context.Context, s LogCreator, workflowID string, fallback hermod.Logger) *DatabaseLogger {
	// The cancel func is retained on the logger and invoked by Close, which the
	// registry calls when a workflow's engine stops.
	ctx, cancel := context.WithCancel(parentCtx) //nolint:gosec // released in Close
	sampleRate := 1.0
	if v := os.Getenv("HERMOD_DB_LOG_SAMPLE_RATE"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			sampleRate = f
		}
	}

	l := &DatabaseLogger{
		storage:    s,
		ctx:        ctx,
		cancel:     cancel,
		workflowID: workflowID,
		fallback:   fallback,
		buffer:     make([]storage.Log, 0, 50),
		sampleRate: sampleRate,
	}
	go l.backgroundFlush()
	return l
}

// Close stops the background flusher and drains what is buffered. It is safe to
// call more than once.
func (l *DatabaseLogger) Close() {
	l.closeOnce.Do(l.cancel)
}

func (l *DatabaseLogger) backgroundFlush() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			l.Flush()
		case <-l.ctx.Done():
			l.Flush()
			return
		}
	}
}

func (l *DatabaseLogger) UpdateStorage(s LogCreator) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.storage = s
}

// flushErrorInterval throttles the report of a failing log flush. A database
// that is down fails every flush, so one line per failure would flood the very
// log the report is trying to reach.
const flushErrorInterval = 5 * time.Minute

func (l *DatabaseLogger) Flush() {
	l.mu.Lock()
	if len(l.buffer) == 0 {
		l.mu.Unlock()
		return
	}
	batch := l.buffer
	l.buffer = make([]storage.Log, 0, 50)
	store := l.storage
	l.mu.Unlock()

	// Use a background context for flushing to ensure it completes
	flushCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := store.CreateLogs(flushCtx, batch); err != nil {
		l.reportFlushFailure(err, len(batch))
	}
}

// reportFlushFailure surfaces a failed database write on the process log.
//
// Discarding this error used to mean a workflow's logs could vanish completely
// with nothing anywhere to say so. The process log is the one destination that
// does not depend on the database being reachable, which is precisely the
// condition under which these writes fail.
func (l *DatabaseLogger) reportFlushFailure(err error, batchSize int) {
	l.mu.Lock()
	now := time.Now()
	if !l.lastFlushErr.IsZero() && now.Sub(l.lastFlushErr) < flushErrorInterval {
		l.mu.Unlock()
		return
	}
	l.lastFlushErr = now
	fallback := l.fallback
	overflowed := l.dropped
	l.dropped = 0
	l.mu.Unlock()

	if fallback == nil {
		return
	}
	fallback.Error("Workflow logs could not be written to storage and were dropped",
		"workflow_id", l.workflowID,
		"dropped_entries", batchSize+overflowed,
		"dropped_to_buffer_overflow", overflowed,
		"error", err.Error(),
		"throttled_for", flushErrorInterval.String())
}

// log fans a line out to the process log and to the database buffer.
//
// The process log is written first and unconditionally. The database copy is
// what powers the per-workflow log view in the UI, but it is buffered, sampled,
// and dependent on log storage actually being configured — in platform-worker
// mode it is not (SetStorage is called without SetLogStorage), so every line a
// running engine emitted was being discarded. An engine's own account of why it
// stopped working is not something to route exclusively through the component
// most likely to be down at the time.
func (l *DatabaseLogger) log(level string, msg string, keysAndValues ...any) {
	l.tee(level, msg, keysAndValues...)

	if (level == "DEBUG" || level == "INFO") && l.sampleRate < 1.0 {
		if rand.Float64() > l.sampleRate {
			return
		}
	}

	logEntry := l.entry(level, msg, keysAndValues...)

	l.mu.Lock()
	if len(l.buffer) >= dbLogBufferMax {
		// The buffer is full because storage is not draining it. Drop the
		// oldest entry rather than the newest: during an incident the most
		// recent lines are the ones that explain it. Growing instead would
		// trade a visibility problem for an out-of-memory one.
		l.buffer = append(l.buffer[:0], l.buffer[1:]...)
		l.dropped++
	}
	l.buffer = append(l.buffer, logEntry)
	isFull := len(l.buffer) >= dbLogBufferFlushAt
	l.mu.Unlock()

	// Flush on a separate goroutine. This runs on whichever engine goroutine
	// emitted the line — a source ingestion loop, a sink writer, a message
	// processor — and a slow log endpoint must not become a slow pipeline. That
	// matters most during an incident, when the engine logs hardest and the
	// storage behind it is most likely to be struggling.
	if isFull && l.flushing.CompareAndSwap(false, true) {
		go func() {
			defer l.flushing.Store(false)
			l.Flush()
		}()
	}
}

// entry builds the stored form of a log line, lifting the structured fields the
// log table has columns for out of the free-form data blob.
func (l *DatabaseLogger) entry(level, msg string, keysAndValues ...any) storage.Log {
	e := storage.Log{
		Timestamp:  time.Now(),
		Level:      level,
		Message:    msg,
		WorkflowID: l.workflowID,
	}

	for i := 0; i+1 < len(keysAndValues); i += 2 {
		key, ok := keysAndValues[i].(string)
		if !ok {
			key = fmt.Sprintf("%v", keysAndValues[i])
		}
		valStr := fmt.Sprintf("%v", keysAndValues[i+1])

		switch key {
		case "workflow_id":
			e.WorkflowID = valStr
		case "source_id":
			e.SourceID = valStr
		case "sink_id":
			e.SinkID = valStr
		case "action":
			e.Action = valStr
		default:
			if e.Data != "" {
				e.Data += ", "
			}
			e.Data += fmt.Sprintf("%s: %s", key, valStr)
		}
	}
	return e
}

// buffered reports how many entries are waiting to be written.
func (l *DatabaseLogger) buffered() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buffer)
}

// tee mirrors a line to the process logger, which applies its own level filter
// and sampling. The workflow id is added when the caller did not supply one, so
// a line is always attributable to a workflow in the process log.
func (l *DatabaseLogger) tee(level, msg string, keysAndValues ...any) {
	l.mu.Lock()
	fallback := l.fallback
	l.mu.Unlock()
	if fallback == nil {
		return
	}

	kv := keysAndValues
	if l.workflowID != "" && !hasKey(keysAndValues, "workflow_id") {
		kv = append(append(make([]any, 0, len(keysAndValues)+2), keysAndValues...), "workflow_id", l.workflowID)
	}

	switch level {
	case "DEBUG":
		fallback.Debug(msg, kv...)
	case "WARN":
		fallback.Warn(msg, kv...)
	case "ERROR":
		fallback.Error(msg, kv...)
	default:
		fallback.Info(msg, kv...)
	}
}

func hasKey(keysAndValues []any, key string) bool {
	for i := 0; i < len(keysAndValues); i += 2 {
		if k, ok := keysAndValues[i].(string); ok && k == key {
			return true
		}
	}
	return false
}

func (l *DatabaseLogger) Debug(msg string, keysAndValues ...any) {
	l.log("DEBUG", msg, keysAndValues...)
}

func (l *DatabaseLogger) Info(msg string, keysAndValues ...any) {
	l.log("INFO", msg, keysAndValues...)
}

func (l *DatabaseLogger) Warn(msg string, keysAndValues ...any) {
	l.log("WARN", msg, keysAndValues...)
}

func (l *DatabaseLogger) Error(msg string, keysAndValues ...any) {
	l.log("ERROR", msg, keysAndValues...)
}
