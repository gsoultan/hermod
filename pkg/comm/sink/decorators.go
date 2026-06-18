package sink

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/user/hermod"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

var tracer = otel.Tracer("hermod-sink")

// TracingSink wraps a Sink and adds OpenTelemetry tracing.
type TracingSink struct {
	hermod.Sink
	sinkID string
}

func NewTracingSink(s hermod.Sink, sinkID string) *TracingSink {
	return &TracingSink{
		Sink:   s,
		sinkID: sinkID,
	}
}

func (s *TracingSink) Write(ctx context.Context, msg hermod.Message) error {
	ctx, span := tracer.Start(ctx, "sink.write", trace.WithAttributes(
		attribute.String("sink_id", s.sinkID),
		attribute.String("message_id", msg.ID()),
	))
	defer span.End()

	err := s.Sink.Write(ctx, msg)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	} else {
		span.SetStatus(codes.Ok, "OK")
	}
	return err
}

func (s *TracingSink) WriteBatch(ctx context.Context, msgs []hermod.Message) error {
	ctx, span := tracer.Start(ctx, "sink.write_batch", trace.WithAttributes(
		attribute.String("sink_id", s.sinkID),
		attribute.Int("batch_size", len(msgs)),
	))
	defer span.End()

	if bs, ok := s.Sink.(hermod.BatchSink); ok {
		err := bs.WriteBatch(ctx, msgs)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		} else {
			span.SetStatus(codes.Ok, "OK")
		}
		return err
	}

	// Fallback
	for _, m := range msgs {
		if err := s.Write(ctx, m); err != nil {
			return err
		}
	}
	return nil
}

func (s *TracingSink) ExecuteSQL(ctx context.Context, query string) ([]map[string]any, error) {
	if se, ok := s.Sink.(hermod.SQLExecutor); ok {
		return se.ExecuteSQL(ctx, query)
	}
	return nil, fmt.Errorf("%w: source does not support SQL execution", hermod.ErrNotSupported)
}

func (s *TracingSink) DiscoverDatabases(ctx context.Context) ([]string, error) {
	if d, ok := s.Sink.(hermod.Discoverer); ok {
		return d.DiscoverDatabases(ctx)
	}
	return nil, fmt.Errorf("sink does not support database discovery")
}

func (s *TracingSink) DiscoverTables(ctx context.Context) ([]string, error) {
	if d, ok := s.Sink.(hermod.Discoverer); ok {
		return d.DiscoverTables(ctx)
	}
	return nil, fmt.Errorf("sink does not support table discovery")
}

func (s *TracingSink) DiscoverColumns(ctx context.Context, table string) ([]hermod.ColumnInfo, error) {
	if d, ok := s.Sink.(hermod.ColumnDiscoverer); ok {
		return d.DiscoverColumns(ctx, table)
	}
	return nil, fmt.Errorf("sink does not support column discovery")
}

// RetrySink wraps a Sink and adds retry logic.
type RetrySink struct {
	hermod.Sink
	maxRetries    int
	retryInterval time.Duration
	logger        hermod.Logger
}

func NewRetrySink(s hermod.Sink, maxRetries int, retryInterval time.Duration, logger hermod.Logger) *RetrySink {
	return &RetrySink{
		Sink:          s,
		maxRetries:    maxRetries,
		retryInterval: retryInterval,
		logger:        logger,
	}
}

func (s *RetrySink) Write(ctx context.Context, msg hermod.Message) error {
	var lastErr error
	maxRetries := max(s.maxRetries, 1)

	for i := range maxRetries {
		err := s.Sink.Write(ctx, msg)
		if err == nil {
			return nil
		}
		lastErr = err
		if s.logger != nil {
			s.logger.Warn("Sink write error, retrying", "attempt", i+1, "error", err)
		}

		// Do not wait after the final attempt; we are about to return the error.
		if i == maxRetries-1 {
			break
		}

		if err := s.waitBeforeRetry(ctx, i); err != nil {
			return err
		}
	}
	return fmt.Errorf("sink write failed after %d retries: %w", maxRetries, lastErr)
}

// waitBeforeRetry sleeps for a jittered, linearly-increasing backoff interval
// based on the attempt index, returning early if the context is cancelled.
func (s *RetrySink) waitBeforeRetry(ctx context.Context, attempt int) error {
	interval := time.Duration(attempt+1) * s.retryInterval
	jitter := 0.8 + rand.Float64()*0.4
	interval = time.Duration(float64(interval) * jitter)

	select {
	case <-time.After(interval):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *RetrySink) WriteBatch(ctx context.Context, msgs []hermod.Message) error {
	bs, ok := s.Sink.(hermod.BatchSink)
	if !ok {
		for _, m := range msgs {
			if err := s.Write(ctx, m); err != nil {
				return err
			}
		}
		return nil
	}

	var lastErr error
	maxRetries := max(s.maxRetries, 1)

	for i := range maxRetries {
		err := bs.WriteBatch(ctx, msgs)
		if err == nil {
			return nil
		}
		lastErr = err
		if s.logger != nil {
			s.logger.Warn("Sink batch write error, retrying", "attempt", i+1, "error", err)
		}

		// Do not wait after the final attempt; we are about to return the error.
		if i == maxRetries-1 {
			break
		}

		if err := s.waitBeforeRetry(ctx, i); err != nil {
			return err
		}
	}
	return fmt.Errorf("sink batch write failed after %d retries: %w", maxRetries, lastErr)
}

func (s *RetrySink) ExecuteSQL(ctx context.Context, query string) ([]map[string]any, error) {
	if se, ok := s.Sink.(hermod.SQLExecutor); ok {
		return se.ExecuteSQL(ctx, query)
	}
	return nil, fmt.Errorf("%w: source does not support SQL execution", hermod.ErrNotSupported)
}

func (s *RetrySink) DiscoverDatabases(ctx context.Context) ([]string, error) {
	if d, ok := s.Sink.(hermod.Discoverer); ok {
		return d.DiscoverDatabases(ctx)
	}
	return nil, fmt.Errorf("sink does not support database discovery")
}

func (s *RetrySink) DiscoverTables(ctx context.Context) ([]string, error) {
	if d, ok := s.Sink.(hermod.Discoverer); ok {
		return d.DiscoverTables(ctx)
	}
	return nil, fmt.Errorf("sink does not support table discovery")
}

func (s *RetrySink) DiscoverColumns(ctx context.Context, table string) ([]hermod.ColumnInfo, error) {
	if d, ok := s.Sink.(hermod.ColumnDiscoverer); ok {
		return d.DiscoverColumns(ctx, table)
	}
	return nil, fmt.Errorf("sink does not support column discovery")
}
