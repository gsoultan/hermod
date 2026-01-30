package engine

import (
	"fmt"
	"os"

	"github.com/rs/zerolog"
)

// DefaultLogger is a simple logger that uses zerolog for zero-allocation structured logging.
type DefaultLogger struct {
	logger zerolog.Logger
}

// NewDefaultLogger creates a DefaultLogger with stderr output and timestamps.
func NewDefaultLogger() *DefaultLogger {
	return &DefaultLogger{
		logger: zerolog.New(os.Stderr).With().Timestamp().Logger(),
	}
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
	l.log(l.logger.Warn(), msg, keysAndValues...)
}

// Error logs an error-level message with structured key/value pairs.
func (l *DefaultLogger) Error(msg string, keysAndValues ...interface{}) {
	l.log(l.logger.Error(), msg, keysAndValues...)
}
