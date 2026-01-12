package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/user/hermod"
)

// DefaultLogger is a simple logger that uses zerolog for zero-allocation structured logging.
type DefaultLogger struct {
	logger zerolog.Logger
}

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

func (l *DefaultLogger) Debug(msg string, keysAndValues ...interface{}) {
	l.log(l.logger.Debug(), msg, keysAndValues...)
}

func (l *DefaultLogger) Info(msg string, keysAndValues ...interface{}) {
	l.log(l.logger.Info(), msg, keysAndValues...)
}

func (l *DefaultLogger) Warn(msg string, keysAndValues ...interface{}) {
	l.log(l.logger.Warn(), msg, keysAndValues...)
}

func (l *DefaultLogger) Error(msg string, keysAndValues ...interface{}) {
	l.log(l.logger.Error(), msg, keysAndValues...)
}

// Engine orchestrates the data flow from Source to Sinks.
type Engine struct {
	source     hermod.Source
	sinks      []hermod.Sink
	buffer     hermod.Producer // Using Producer as a buffer
	logger     hermod.Logger
	config     Config
	transforms hermod.Transformer

	connectionID string
	sourceID     string
	sinkIDs      []string
}

// Config holds configuration for the Engine.
type Config struct {
	MaxRetries    int
	RetryInterval time.Duration
}

// DefaultConfig returns the default configuration for the Engine.
func DefaultConfig() Config {
	return Config{
		MaxRetries:    3,
		RetryInterval: 100 * time.Millisecond,
	}
}

func NewEngine(source hermod.Source, sinks []hermod.Sink, buffer hermod.Producer) *Engine {
	return &Engine{
		source: source,
		sinks:  sinks,
		buffer: buffer,
		logger: NewDefaultLogger(),
		config: DefaultConfig(),
	}
}

// SetConfig sets the configuration for the engine.
func (e *Engine) SetConfig(config Config) {
	e.config = config
}

// SetLogger sets the logger for the engine.
func (e *Engine) SetLogger(logger hermod.Logger) {
	e.logger = logger
}

// SetTransformations sets the transformations to be applied to messages.
func (e *Engine) SetTransformations(t hermod.Transformer) {
	e.transforms = t
}

// SetIDs sets the IDs for connection, source and sinks.
func (e *Engine) SetIDs(connectionID string, sourceID string, sinkIDs []string) {
	e.connectionID = connectionID
	e.sourceID = sourceID
	e.sinkIDs = sinkIDs
}

// Start begins the data transfer process.
func (e *Engine) Start(ctx context.Context) error {
	var wg sync.WaitGroup
	errCh := make(chan error, 2)

	e.logger.Info("Starting Hermod Engine", "connection_id", e.connectionID)

	// Pre-flight checks
	if err := e.source.Ping(ctx); err != nil {
		return fmt.Errorf("source ping failed: %w", err)
	}
	for i, snk := range e.sinks {
		if err := snk.Ping(ctx); err != nil {
			return fmt.Errorf("sink %d ping failed: %w", i, err)
		}
	}

	// Source to Buffer
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				e.logger.Info("Source-to-Buffer worker stopping due to context cancellation", "connection_id", e.connectionID)
				return
			default:
				msg, err := e.source.Read(ctx)
				if err != nil {
					if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
						e.logger.Error("Source read error", "connection_id", e.connectionID, "error", err)
						errCh <- fmt.Errorf("source read error: %w", err)
					}
					return
				}

				e.logger.Info("Message received from source",
					"connection_id", e.connectionID,
					"source_id", e.sourceID,
					"action", "read",
					"message_id", msg.ID(),
					"table", msg.Table(),
					"operation", msg.Operation(),
					"payload", string(msg.Payload()),
					"before", string(msg.Before()),
					"after", string(msg.After()),
				)

				originalID := msg.ID()
				if e.transforms != nil {
					msg, err = e.transforms.Transform(ctx, msg)
					if err != nil {
						e.logger.Error("Transformation error", "connection_id", e.connectionID, "error", err)
						// Continue or stop? Let's stop for safety if transformation fails
						errCh <- fmt.Errorf("transformation error: %w", err)
						return
					}
					if msg == nil {
						e.logger.Info("Message filtered out by transformation",
							"connection_id", e.connectionID,
							"source_id", e.sourceID,
							"action", "filter",
							"message_id", originalID,
						)
						continue
					}
					e.logger.Info("Message transformed",
						"connection_id", e.connectionID,
						"source_id", e.sourceID,
						"action", "transform",
						"message_id", msg.ID(),
						"payload", string(msg.Payload()),
						"before", string(msg.Before()),
						"after", string(msg.After()),
					)
				}
				if err := e.buffer.Produce(ctx, msg); err != nil {
					if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
						e.logger.Error("Buffer produce error", "connection_id", e.connectionID, "error", err)
						errCh <- fmt.Errorf("buffer produce error: %w", err)
					}
					return
				}
			}
		}
	}()

	// Wait for Source worker to finish then close buffer
	go func() {
		wg.Wait()
		// If buffer implements Closer, close it to signal Consumer that no more messages are coming
		if closer, ok := e.buffer.(interface{ Close() error }); ok {
			if err := closer.Close(); err != nil {
				e.logger.Error("Error closing buffer", "connection_id", e.connectionID, "error", err)
			}
		}
	}()

	// Buffer to Sink
	var sinkWg sync.WaitGroup
	sinkWg.Add(1)
	go func() {
		defer sinkWg.Done()
		consumer, ok := e.buffer.(hermod.Consumer)
		if !ok {
			e.logger.Error("Buffer does not implement Consumer interface", "connection_id", e.connectionID)
			errCh <- fmt.Errorf("buffer does not implement Consumer interface")
			return
		}

		// Use a separate context for draining to ensure we don't get stuck if sink is slow,
		// but also allow it to finish even if main context is cancelled.
		drainCtx := context.Background()

		err := consumer.Consume(drainCtx, func(drainCtx context.Context, msg hermod.Message) error {
			for i, snk := range e.sinks {
				sinkID := ""
				if i < len(e.sinkIDs) {
					sinkID = e.sinkIDs[i]
				}

				currentMsg := msg
				// Retry mechanism for Sink Write
				var lastErr error
				for j := 0; j < e.config.MaxRetries; j++ {
					if err := snk.Write(drainCtx, currentMsg); err != nil {
						lastErr = err
						e.logger.Warn("Sink write error, retrying", "connection_id", e.connectionID, "attempt", j+1, "sink_id", sinkID, "error", err)

						select {
						case <-time.After(time.Duration(j+1) * e.config.RetryInterval):
							continue
						case <-ctx.Done():
							return ctx.Err()
						}
					}
					e.logger.Info("Message written to sink",
						"connection_id", e.connectionID,
						"sink_id", sinkID,
						"action", "write",
						"message_id", currentMsg.ID(),
						"payload", string(currentMsg.Payload()),
						"before", string(currentMsg.Before()),
						"after", string(currentMsg.After()),
					)
					lastErr = nil
					break
				}
				if lastErr != nil {
					e.logger.Error("Sink write failed after retries", "connection_id", e.connectionID, "error", lastErr)
					return fmt.Errorf("sink write error: %w", lastErr)
				}
			}

			// Acknowledge the message to the source after all successful sink writes
			if err := e.source.Ack(drainCtx, msg); err != nil {
				e.logger.Error("Source acknowledgement failed", "connection_id", e.connectionID, "error", err)
				return fmt.Errorf("source acknowledgement failed: %w", err)
			}

			return nil
		})
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			e.logger.Error("Buffer-to-Sink worker error", "connection_id", e.connectionID, "error", err)
			errCh <- err
		} else {
			e.logger.Info("Buffer-to-Sink worker stopping", "connection_id", e.connectionID)
		}
	}()

	sinkWg.Wait()
	close(errCh)

	var lastErr error
	for err := range errCh {
		if err != nil {
			lastErr = err
		}
	}

	if lastErr != nil {
		e.logger.Error("Hermod Engine stopped with error", "connection_id", e.connectionID, "error", lastErr)
		return lastErr
	}

	e.logger.Info("Hermod Engine stopped gracefully", "connection_id", e.connectionID)
	return nil
}
