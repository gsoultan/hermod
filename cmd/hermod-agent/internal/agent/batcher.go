// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package agent

import (
	"context"
	"log/slog"
	"time"

	"github.com/gsoultan/hermod/cmd/hermod-agent/internal/spool"
)

// batcher groups records from all devices into one spool write, so a burst
// costs one disk sync rather than one per record. A record is never held for
// longer than flushEvery.
type batcher struct {
	in         chan spool.Record
	sp         *spool.Spool
	log        *slog.Logger
	max        int
	flushEvery time.Duration
	done       chan struct{}
}

func newBatcher(sp *spool.Spool, log *slog.Logger) *batcher {
	return &batcher{in: make(chan spool.Record, 1024), sp: sp, log: log, max: 256, flushEvery: 20 * time.Millisecond, done: make(chan struct{})}
}

// add queues a record. It blocks when the queue is full, which slows the
// device reader instead of growing memory.
func (b *batcher) add(ctx context.Context, r spool.Record) {
	select {
	case b.in <- r:
	case <-ctx.Done():
	}
}

// run writes queued records until ctx ends, then writes what is left.
func (b *batcher) run(ctx context.Context) {
	defer close(b.done)
	var pending []spool.Record
	t := time.NewTimer(b.flushEvery)
	defer t.Stop()
	for {
		select {
		case r := <-b.in:
			pending = append(pending, r)
			if len(pending) < b.max {
				continue
			}
		case <-t.C:
		case <-ctx.Done():
			pending = b.drain(pending)
			b.flush(pending)
			return
		}
		pending = b.flush(pending)
		t.Reset(b.flushEvery)
	}
}

func (b *batcher) drain(pending []spool.Record) []spool.Record {
	for {
		select {
		case r := <-b.in:
			pending = append(pending, r)
		default:
			return pending
		}
	}
}

func (b *batcher) flush(pending []spool.Record) []spool.Record {
	if len(pending) == 0 {
		return pending
	}
	if _, err := b.sp.Append(pending); err != nil {
		b.log.Error("spool write failed; records lost", "records", len(pending), "err", err)
	}
	return pending[:0]
}
