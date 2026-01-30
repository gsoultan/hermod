package failover

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/user/hermod"
)

// FailoverSink wraps a primary sink and multiple fallback sinks.
// It implements hermod.Sink, hermod.BatchSink and hermod.Loggable.
type FailoverSink struct {
	primary   hermod.Sink
	fallbacks []hermod.Sink
	logger    hermod.Logger
	strategy  string // "failover" (default), "round-robin"
	counter   uint64 // For round-robin
}

func NewFailoverSink(primary hermod.Sink, fallbacks []hermod.Sink) *FailoverSink {
	return &FailoverSink{
		primary:   primary,
		fallbacks: fallbacks,
		strategy:  "failover",
	}
}

func NewFailoverSinkWithStrategy(primary hermod.Sink, fallbacks []hermod.Sink, strategy string) *FailoverSink {
	return &FailoverSink{
		primary:   primary,
		fallbacks: fallbacks,
		strategy:  strategy,
	}
}

func (s *FailoverSink) Write(ctx context.Context, msg hermod.Message) error {
	if s.strategy == "round-robin" {
		return s.writeRoundRobin(ctx, msg)
	}
	return s.writeFailover(ctx, msg)
}

func (s *FailoverSink) writeFailover(ctx context.Context, msg hermod.Message) error {
	err := s.primary.Write(ctx, msg)
	if err == nil {
		return nil
	}

	if s.logger != nil {
		s.logger.Warn("Primary sink failed, trying fallbacks", "error", err)
	}

	for i, fallback := range s.fallbacks {
		err = fallback.Write(ctx, msg)
		if err == nil {
			if s.logger != nil {
				s.logger.Info("Fallback sink succeeded", "index", i)
			}
			return nil
		}
		if s.logger != nil {
			s.logger.Warn("Fallback sink failed", "index", i, "error", err)
		}
	}

	return fmt.Errorf("all sinks in failover group failed: %w", err)
}

func (s *FailoverSink) writeRoundRobin(ctx context.Context, msg hermod.Message) error {
	total := len(s.fallbacks) + 1
	idx := int(atomic.AddUint64(&s.counter, 1) % uint64(total))

	var target hermod.Sink
	if idx == 0 {
		target = s.primary
	} else {
		target = s.fallbacks[idx-1]
	}

	err := target.Write(ctx, msg)
	if err == nil {
		return nil
	}

	// If selected target fails, fallback to sequential failover for this message
	if s.logger != nil {
		s.logger.Warn("Round-robin target failed, falling back to sequential", "index", idx, "error", err)
	}
	return s.writeFailover(ctx, msg)
}

func (s *FailoverSink) WriteBatch(ctx context.Context, msgs []hermod.Message) error {
	if s.strategy == "round-robin" {
		return s.writeBatchRoundRobin(ctx, msgs)
	}
	return s.writeBatchFailover(ctx, msgs)
}

func (s *FailoverSink) writeBatchFailover(ctx context.Context, msgs []hermod.Message) error {
	// Try primary as BatchSink if it supports it
	if bs, ok := s.primary.(hermod.BatchSink); ok {
		err := bs.WriteBatch(ctx, msgs)
		if err == nil {
			return nil
		}
	} else {
		// Fallback to individual writes if primary doesn't support batching
		allOk := true
		for _, msg := range msgs {
			if err := s.primary.Write(ctx, msg); err != nil {
				allOk = false
				break
			}
		}
		if allOk {
			return nil
		}
	}

	if s.logger != nil {
		s.logger.Warn("Primary sink batch write failed, trying fallbacks")
	}

	for i, fallback := range s.fallbacks {
		if bs, ok := fallback.(hermod.BatchSink); ok {
			err := bs.WriteBatch(ctx, msgs)
			if err == nil {
				return nil
			}
		} else {
			allOk := true
			for _, msg := range msgs {
				if err := fallback.Write(ctx, msg); err != nil {
					allOk = false
					break
				}
			}
			if allOk {
				return nil
			}
		}
		if s.logger != nil {
			s.logger.Warn("Fallback sink batch write failed", "index", i)
		}
	}

	return fmt.Errorf("all sinks in failover group failed batch write")
}

func (s *FailoverSink) writeBatchRoundRobin(ctx context.Context, msgs []hermod.Message) error {
	total := len(s.fallbacks) + 1
	idx := int(atomic.AddUint64(&s.counter, 1) % uint64(total))

	var target hermod.Sink
	if idx == 0 {
		target = s.primary
	} else {
		target = s.fallbacks[idx-1]
	}

	var err error
	if bs, ok := target.(hermod.BatchSink); ok {
		err = bs.WriteBatch(ctx, msgs)
	} else {
		allOk := true
		for _, msg := range msgs {
			if e := target.Write(ctx, msg); e != nil {
				allOk = false
				err = e
				break
			}
		}
		if allOk {
			return nil
		}
	}

	if err == nil {
		return nil
	}

	if s.logger != nil {
		s.logger.Warn("Round-robin batch target failed, falling back to sequential", "index", idx, "error", err)
	}
	return s.writeBatchFailover(ctx, msgs)
}

func (s *FailoverSink) Ping(ctx context.Context) error {
	// Ping primary to check overall health
	return s.primary.Ping(ctx)
}

func (s *FailoverSink) Close() error {
	var lastErr error
	if err := s.primary.Close(); err != nil {
		lastErr = err
	}
	for _, fallback := range s.fallbacks {
		if err := fallback.Close(); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

func (s *FailoverSink) SetLogger(logger hermod.Logger) {
	s.logger = logger
	if l, ok := s.primary.(hermod.Loggable); ok {
		l.SetLogger(logger)
	}
	for _, fallback := range s.fallbacks {
		if l, ok := fallback.(hermod.Loggable); ok {
			l.SetLogger(logger)
		}
	}
}
