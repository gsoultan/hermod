package telemetry

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/rs/zerolog"
)

// DefaultLogger is a simple logger that uses zerolog for zero-allocation structured logging.
type DefaultLogger struct {
	logger  zerolog.Logger
	sampler zerolog.Sampler
	sampled zerolog.Logger
}

// NewDefaultLogger creates a DefaultLogger with stderr output and timestamps.
func NewDefaultLogger() *DefaultLogger {
	output := os.Stderr
	level := zerolog.InfoLevel

	if v := os.Getenv("HERMOD_LOG_LEVEL"); v != "" {
		switch strings.ToLower(v) {
		case "debug":
			level = zerolog.DebugLevel
		case "info":
			level = zerolog.InfoLevel
		case "warn":
			level = zerolog.WarnLevel
		case "error":
			level = zerolog.ErrorLevel
		}
	}

	l := zerolog.New(output).Level(level).With().Timestamp().Logger()

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

func (l *DefaultLogger) log(event *zerolog.Event, msg string, keysAndValues ...any) {
	if event == nil || !event.Enabled() {
		return
	}

	for i := 0; i < len(keysAndValues); i += 2 {
		key, ok := keysAndValues[i].(string)
		if !ok {
			key = fmt.Sprintf("%v", keysAndValues[i])
		}

		if i+1 < len(keysAndValues) {
			// error values must be rendered via their Error() string. Passing an
			// error to Interface JSON-marshals it to "{}" (errors have no
			// exported fields), which silently drops the message.
			if errVal, ok := keysAndValues[i+1].(error); ok {
				event.AnErr(key, errVal)
			} else {
				event.Interface(key, keysAndValues[i+1])
			}
		} else {
			event.Interface(key, nil)
		}
	}
	event.Msg(msg)
}

// Debug logs a debug-level message with structured key/value pairs.
func (l *DefaultLogger) Debug(msg string, keysAndValues ...any) {
	l.log(l.logger.Debug(), msg, keysAndValues...)
}

// Info logs an info-level message with structured key/value pairs.
func (l *DefaultLogger) Info(msg string, keysAndValues ...any) {
	l.log(l.logger.Info(), msg, keysAndValues...)
}

// Warn logs a warning-level message with structured key/value pairs.
func (l *DefaultLogger) Warn(msg string, keysAndValues ...any) {
	if l.sampler != nil {
		l.log(l.sampled.Warn(), msg, keysAndValues...)
		return
	}
	l.log(l.logger.Warn(), msg, keysAndValues...)
}

// Error logs an error-level message with structured key/value pairs.
func (l *DefaultLogger) Error(msg string, keysAndValues ...any) {
	if l.sampler != nil {
		l.log(l.sampled.Error(), msg, keysAndValues...)
		return
	}
	l.log(l.logger.Error(), msg, keysAndValues...)
}
