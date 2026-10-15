// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package sink

import (
	"context"
	"sync"
	"time"

	"github.com/gsoultan/hermod"
)

// BatchBufferSink coalesces messages before writing to the underlying sink.
type BatchBufferSink struct {
	hermod.Sink
	batchSize   int
	maxWaitTime time.Duration
	buffer      []hermod.Message
	mu          sync.Mutex
	ctx         context.Context
	cancel      context.CancelFunc
}

func NewBatchBufferSink(s hermod.Sink, batchSize int, maxWaitTime time.Duration) *BatchBufferSink {
	ctx, cancel := context.WithCancel(context.Background())
	bbs := &BatchBufferSink{
		Sink:        s,
		batchSize:   batchSize,
		maxWaitTime: maxWaitTime,
		buffer:      make([]hermod.Message, 0, batchSize),
		ctx:         ctx,
		cancel:      cancel,
	}
	go bbs.backgroundFlush()
	return bbs
}

func (s *BatchBufferSink) Write(ctx context.Context, msg hermod.Message) error {
	s.mu.Lock()
	s.buffer = append(s.buffer, msg)
	if len(s.buffer) >= s.batchSize {
		msgs := s.takeBuffer()
		s.mu.Unlock()
		return s.flush(ctx, msgs)
	}
	s.mu.Unlock()
	return nil
}

func (s *BatchBufferSink) flush(ctx context.Context, msgs []hermod.Message) error {
	if len(msgs) == 0 {
		return nil
	}
	if bs, ok := s.Sink.(hermod.BatchSink); ok {
		return bs.WriteBatch(ctx, msgs)
	}
	for _, m := range msgs {
		if err := s.Sink.Write(ctx, m); err != nil {
			return err
		}
	}
	return nil
}

func (s *BatchBufferSink) takeBuffer() []hermod.Message {
	msgs := s.buffer
	s.buffer = make([]hermod.Message, 0, s.batchSize)
	return msgs
}

func (s *BatchBufferSink) backgroundFlush() {
	ticker := time.NewTicker(s.maxWaitTime)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.mu.Lock()
			msgs := s.takeBuffer()
			s.mu.Unlock()
			if len(msgs) > 0 {
				_ = s.flush(s.ctx, msgs)
			}
		case <-s.ctx.Done():
			return
		}
	}
}

func (s *BatchBufferSink) Close() error {
	s.cancel()
	s.mu.Lock()
	msgs := s.takeBuffer()
	s.mu.Unlock()
	if len(msgs) > 0 {
		_ = s.flush(context.Background(), msgs)
	}
	if closer, ok := s.Sink.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}
