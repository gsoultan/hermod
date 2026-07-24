package batcher

import (
	"context"
	"sync"
	"time"
)

// Result represents the outcome of a batched operation.
type Result[V any] struct {
	Value V
	Error error
}

// Batcher coalesces individual requests into batches for more efficient processing.
type Batcher[K comparable, V any] struct {
	mu           sync.Mutex
	pending      map[K][]chan Result[V]
	maxBatchSize int
	maxWaitTime  time.Duration
	batchFn      func(ctx context.Context, keys []K) (map[K]V, error)
	timer        *time.Timer
	closed       bool
}

// NewBatcher creates a new Batcher.
func NewBatcher[K comparable, V any](maxSize int, maxWait time.Duration, fn func(ctx context.Context, keys []K) (map[K]V, error)) *Batcher[K, V] {
	if maxSize <= 0 {
		maxSize = 100
	}
	if maxWait <= 0 {
		maxWait = 10 * time.Millisecond
	}
	return &Batcher[K, V]{
		pending:      make(map[K][]chan Result[V]),
		maxBatchSize: maxSize,
		maxWaitTime:  maxWait,
		batchFn:      fn,
	}
}

// Execute adds a key to the current batch and waits for the result.
func (b *Batcher[K, V]) Execute(ctx context.Context, key K) (V, error) {
	resChan := make(chan Result[V], 1)

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		var zero V
		return zero, context.Canceled
	}

	b.pending[key] = append(b.pending[key], resChan)

	startFlush := false
	if len(b.pending) == 1 {
		b.timer = time.AfterFunc(b.maxWaitTime, b.flush)
	}

	if len(b.pending) >= b.maxBatchSize {
		if b.timer != nil {
			b.timer.Stop()
			b.timer = nil
		}
		startFlush = true
	}
	b.mu.Unlock()

	if startFlush {
		go b.flush()
	}

	select {
	case res := <-resChan:
		return res.Value, res.Error
	case <-ctx.Done():
		var zero V
		return zero, ctx.Err()
	}
}

func (b *Batcher[K, V]) flush() {
	b.mu.Lock()
	if len(b.pending) == 0 {
		b.mu.Unlock()
		return
	}
	pending := b.pending
	b.pending = make(map[K][]chan Result[V])
	if b.timer != nil {
		b.timer.Stop()
		b.timer = nil
	}
	b.mu.Unlock()

	keys := make([]K, 0, len(pending))
	for k := range pending {
		keys = append(keys, k)
	}

	// Use a background context for the actual batch operation to ensure it
	// finishes even if some individual callers cancel their contexts.
	results, err := b.batchFn(context.Background(), keys)

	for k, chans := range pending {
		var res Result[V]
		if err != nil {
			res.Error = err
		} else {
			if val, ok := results[k]; ok {
				res.Value = val
			}
		}
		for _, ch := range chans {
			ch <- res
		}
	}
}

// Close flushes any pending requests and stops the batcher.
func (b *Batcher[K, V]) Close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	if b.timer != nil {
		b.timer.Stop()
		b.timer = nil
	}
	b.mu.Unlock()
	b.flush()
}
