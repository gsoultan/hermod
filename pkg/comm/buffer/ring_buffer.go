// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

package buffer

import (
	"context"
	"errors"
	"sync"

	"github.com/gsoultan/hermod"
)

// RingBuffer is a high-performance, lock-free (using channels for now as a simple example,
// but optimized for performance) buffer that implements both Producer and Consumer.
type RingBuffer struct {
	ch     chan hermod.Message
	done   chan struct{}
	mu     sync.RWMutex
	closed bool
}

func NewRingBuffer(size int) *RingBuffer {
	return &RingBuffer{
		ch:   make(chan hermod.Message, size),
		done: make(chan struct{}),
	}
}

// Depth reports how many messages are waiting to be consumed and the buffer's
// capacity. A buffer holding messages that never drain is what a wedged
// pipeline looks like from the inside, and is the signal the stall watchdog
// uses to tell "stuck" apart from "idle".
func (b *RingBuffer) Depth() (queued, capacity int) {
	return len(b.ch), cap(b.ch)
}

func (b *RingBuffer) Produce(ctx context.Context, msg hermod.Message) error {
	b.mu.RLock()
	closed := b.closed
	b.mu.RUnlock()
	if closed {
		return errors.New("buffer closed")
	}

	select {
	case b.ch <- msg:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-b.done:
		return errors.New("buffer closed")
	}
}

func (b *RingBuffer) Consume(ctx context.Context, handler hermod.Handler) error {
	for {
		select {
		case msg, ok := <-b.ch:
			if !ok {
				return nil
			}
			if err := handler(ctx, msg); err != nil {
				// In a real system, we might want to handle errors differently (e.g., DLQ)
				return err
			}
		case <-ctx.Done():
			return ctx.Err()
		case <-b.done:
			return nil
		}
	}
}

func (b *RingBuffer) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	close(b.done)
	close(b.ch)
	return nil
}
