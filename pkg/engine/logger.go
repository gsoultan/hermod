package engine

import (
	"fmt"
	"os"
	"strconv"

	"github.com/rs/zerolog"
)

// DefaultLogger is a simple logger that uses zerolog for zero-allocation structured logging.
type DefaultLogger struct {
	logger zerolog.Logger
	// optional sampler to reduce log spam (e.g., Warn/Error)
	sampler zerolog.Sampler
	sampled zerolog.Logger
}

// NewDefaultLogger creates a DefaultLogger with stderr output and timestamps.
func NewDefaultLogger() *DefaultLogger {
	l := zerolog.New(os.Stderr).With().Timestamp().Logger()
	var samp zerolog.Sampler
	if v := os.Getenv("HERMOD_LOG_SAMPLE_N"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 1 {
			samp = zerolog.RandomSampler(n)
		}
	}
	var sampled zerolog.Logger
	if samp != nil {
		sampled = l.Sample(samp)
	}
	return &DefaultLogger{logger: l, sampler: samp, sampled: sampled}
}

func (l *DefaultLogger) log(event *zerolog.Event, msg string, keysAndValues ...interface{}) {
	for i := 0; i < len(keysAndValues); i += 2 {
		key := fmt.Sprintf("%v", keysAndValues[i])
		if i+1 < len(keysAndValues) {
			event.Interface(key, keysAndValues[i+1])
		} else {
			event.Interface(key, nil)
		}
	}
	event.Msg(msg)
}

// Debug logs a debug-level message with structured key/value pairs.
func (l *DefaultLogger) Debug(msg string, keysAndValues ...interface{}) {
	l.log(l.logger.Debug(), msg, keysAndValues...)
}

// Info logs an info-level message with structured key/value pairs.
func (l *DefaultLogger) Info(msg string, keysAndValues ...interface{}) {
	l.log(l.logger.Info(), msg, keysAndValues...)
}

// Warn logs a warning-level message with structured key/value pairs.
func (l *DefaultLogger) Warn(msg string, keysAndValues ...interface{}) {
	if l.sampler != nil {
		l.log(l.sampled.Warn(), msg, keysAndValues...)
		return
	}
	l.log(l.logger.Warn(), msg, keysAndValues...)
}

// Error logs an error-level message with structured key/value pairs.
func (l *DefaultLogger) Error(msg string, keysAndValues ...interface{}) {
	if l.sampler != nil {
		l.log(l.sampled.Error(), msg, keysAndValues...)
		return
	}
	l.log(l.logger.Error(), msg, keysAndValues...)
}
