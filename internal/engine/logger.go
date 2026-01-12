package engine

import (
	"context"
	"fmt"
	"github.com/user/hermod/internal/storage"
)

type DatabaseLogger struct {
	storage storage.Storage
	ctx     context.Context
}

func NewDatabaseLogger(ctx context.Context, s storage.Storage) *DatabaseLogger {
	return &DatabaseLogger{
		storage: s,
		ctx:     ctx,
	}
}

func (l *DatabaseLogger) log(level string, msg string, keysAndValues ...interface{}) {
	log := storage.Log{
		Level:   level,
		Message: msg,
	}

	for i := 0; i < len(keysAndValues); i += 2 {
		if i+1 >= len(keysAndValues) {
			break
		}
		key, ok := keysAndValues[i].(string)
		if !ok {
			continue
		}
		val := keysAndValues[i+1]
		valStr := fmt.Sprintf("%v", val)

		switch key {
		case "connection_id":
			log.ConnectionID = valStr
		case "source_id":
			log.SourceID = valStr
		case "sink_id":
			log.SinkID = valStr
		case "action":
			log.Action = valStr
		default:
			if log.Data != "" {
				log.Data += ", "
			}
			log.Data += fmt.Sprintf("%s: %s", key, valStr)
		}
	}

	_ = l.storage.CreateLog(l.ctx, log)
}

func (l *DatabaseLogger) Debug(msg string, keysAndValues ...interface{}) {
	l.log("DEBUG", msg, keysAndValues...)
}

func (l *DatabaseLogger) Info(msg string, keysAndValues ...interface{}) {
	l.log("INFO", msg, keysAndValues...)
}

func (l *DatabaseLogger) Warn(msg string, keysAndValues ...interface{}) {
	l.log("WARN", msg, keysAndValues...)
}

func (l *DatabaseLogger) Error(msg string, keysAndValues ...interface{}) {
	l.log("ERROR", msg, keysAndValues...)
}
