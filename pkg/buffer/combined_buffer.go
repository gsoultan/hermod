package buffer

import (
	"context"
	"errors"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/compression"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// CombinedBuffer is a two-tier buffer: in-memory ring for speed and file-backed
// buffer for durability and overflow. It implements hermod.Producer and can be
// consumed by the engine like other buffers.
//
// Policy (simple/default):
//   - Produce tries ring first with a short timeout; on timeout or when ring is
//     above spillHigh watermark, it appends to the file buffer.
//   - Consume drains ring first; when empty, it drains from the file buffer and
//     forwards to the handler.
//   - Close signals both tiers; file buffer is responsible for persisting state.
type CombinedBuffer struct {
	ring      *RingBuffer
	file      *FileBuffer
	cap       int
	spillHigh int // when ring length >= spillHigh, prefer spill to file
	spillLow  int // not used in this simple version, reserved for future paging
	produceTO time.Duration

	mu     sync.RWMutex
	closed bool
}

// CombinedOptions holds optional tuning parameters for CombinedBuffer.
type CombinedOptions struct {
	// SpillHighPct and SpillLowPct are percentages of ring capacity (0-100).
	SpillHighPct int
	SpillLowPct  int
	// ProduceTimeout bounds the time we wait to enqueue into the ring.
	ProduceTimeout time.Duration
	// Compressor is used for file-backed storage.
	Compressor compression.Compressor
}

// NewCombinedBuffer constructs a CombinedBuffer.
// ringCapacity: size of in-memory ring channel.
// dir: directory for file buffer; created if missing.
// fileSize: logical size/backpressure window used by FileBuffer.
func NewCombinedBuffer(ringCapacity int, dir string, fileSize int, opts *CombinedOptions) (*CombinedBuffer, error) {
	if ringCapacity <= 0 {
		ringCapacity = 1024
	}
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "hermod-buffer")
	}
	if fileSize < 0 {
		fileSize = 0
	}
	rb := NewRingBuffer(ringCapacity)

	var comp compression.Compressor
	spillHighPct := 80
	spillLowPct := 50
	to := 5 * time.Millisecond
	if opts != nil {
		comp = opts.Compressor
		if opts.SpillHighPct > 0 {
			spillHighPct = opts.SpillHighPct
		}
		if opts.SpillLowPct > 0 {
			spillLowPct = opts.SpillLowPct
		}
		if opts.ProduceTimeout > 0 {
			to = opts.ProduceTimeout
		}
	}

	fb, err := NewFileBufferWithCompressor(dir, fileSize, comp)
	if err != nil {
		return nil, err
	}

	cb := &CombinedBuffer{
		ring:      rb,
		file:      fb,
		cap:       ringCapacity,
		spillHigh: (ringCapacity * spillHighPct) / 100,
		spillLow:  (ringCapacity * spillLowPct) / 100,
		produceTO: to,
	}
	return cb, nil
}

// Produce enqueues the message; tries ring first, otherwise spills to file.
func (b *CombinedBuffer) Produce(ctx context.Context, msg hermod.Message) error {
	// Hold a read lock while attempting the ring send to prevent races with Close()
	// closing the underlying channel. This ensures we never send on a closed channel.
	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return errors.New("buffer closed")
	}

	// Fast path: if ring likely has capacity below high watermark, try send with timeout.
	// We don’t have direct length without exposing internals; use timed select.
	select {
	case b.ring.ch <- msg:
		b.mu.RUnlock()
		return nil
	case <-time.After(b.produceTO):
		// Fall through to spill
		b.mu.RUnlock()
	case <-ctx.Done():
		b.mu.RUnlock()
		return ctx.Err()
	}

	// Spill to file for durability/overflow.
	return b.file.Produce(ctx, msg)
}

// Consume prioritizes draining the ring; when empty, drains from the file buffer.
func (b *CombinedBuffer) Consume(ctx context.Context, handler hermod.Handler) error {
	// We implement a loop that first drains ring non-blocking, then pulls from file.
	for {
		// 1) Drain ring until it’s empty or context closes.
		drained := false
		for {
			select {
			case msg, ok := <-b.ring.ch:
				if !ok {
					drained = true
					break
				}
				if err := handler(ctx, msg); err != nil {
					return err
				}
				drained = true
			default:
				// ring empty
				goto fileDrain
			}
		}

	fileDrain:
		if drained {
			// Loop back to check ring again quickly
		}

		// 2) If ring is empty, consume one batch/item from file buffer.
		// FileBuffer.Consume blocks and continues until context/done.
		// To interleave, we consume with a short-lived context to process available messages.
		// This keeps priority for freshly produced ring items.
		fileCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		err := b.file.Consume(fileCtx, func(c context.Context, m hermod.Message) error {
			return handler(c, m)
		})
		cancel()
		if err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
			return err
		}

		// Check for termination
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Small sleep to prevent tight loop when both are empty.
		time.Sleep(5 * time.Millisecond)
	}
}

// Close closes both tiers.
func (b *CombinedBuffer) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	b.mu.Unlock()

	_ = b.ring.Close()
	return b.file.Close()
}
