package registry

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/user/hermod/internal/storage"
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

	mu         sync.Mutex
	buffer     []storage.Log
	sampleRate float64
}

func NewDatabaseLogger(parentCtx context.Context, s LogCreator, workflowID string) *DatabaseLogger {
	ctx, cancel := context.WithCancel(parentCtx)
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
		buffer:     make([]storage.Log, 0, 50),
		sampleRate: sampleRate,
	}
	go l.backgroundFlush()
	return l
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
	_ = store.CreateLogs(flushCtx, batch)
}

func (l *DatabaseLogger) log(level string, msg string, keysAndValues ...any) {
	if (level == "DEBUG" || level == "INFO") && l.sampleRate < 1.0 {
		if rand.Float64() > l.sampleRate {
			return
		}
	}

	logEntry := storage.Log{
		Timestamp:  time.Now(),
		Level:      level,
		Message:    msg,
		WorkflowID: l.workflowID,
	}

	for i := 0; i < len(keysAndValues); i += 2 {
		if i+1 >= len(keysAndValues) {
			break
		}
		key, ok := keysAndValues[i].(string)
		if !ok {
			key = fmt.Sprintf("%v", keysAndValues[i])
		}
		val := keysAndValues[i+1]
		valStr := fmt.Sprintf("%v", val)

		switch key {
		case "workflow_id":
			logEntry.WorkflowID = valStr
		case "source_id":
			logEntry.SourceID = valStr
		case "sink_id":
			logEntry.SinkID = valStr
		case "action":
			logEntry.Action = valStr
		default:
			if logEntry.Data != "" {
				logEntry.Data += ", "
			}
			logEntry.Data += fmt.Sprintf("%s: %s", key, valStr)
		}
	}

	l.mu.Lock()
	l.buffer = append(l.buffer, logEntry)
	isFull := len(l.buffer) >= 50
	l.mu.Unlock()

	if isFull {
		l.Flush()
	}
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
