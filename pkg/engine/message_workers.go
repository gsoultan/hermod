// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package engine

import (
	"sync/atomic"

	"github.com/gsoultan/hermod"
)

// messageWorkers is an engine's pool of message workers, started as work
// arrives rather than all at once.
//
// Every engine used to start its whole pool -- cap(inFlightSem), 128 by
// default -- as it started, each worker with its own keyed queue, whether or
// not a message ever arrived. That was 128 of the 139 goroutines an idle
// workflow cost, and most of its heap (internal/test/scale, BENCHMARKS.md). A
// worker that hosts many workflows hosts mostly idle ones, so the pool now
// starts empty and grows:
//
//   - a message with an ordering key starts the worker that owns its key, if
//     it is not running yet. Ownership is still the key's hash, so every change
//     to one row goes to one worker in the order it was read, exactly as before;
//   - a message without one goes to the shared queue, and starts one more
//     worker only when none is waiting for work, up to the limit.
//
// It never shrinks: a workflow that was busy once keeps the workers it grew,
// as it did before. The point is that a workflow which is never busy -- the
// common case among a great many -- never pays for them.
//
// queueFor is called from one goroutine only, the buffer's Consume loop, which
// calls its handler sequentially in every implementation (RingBuffer,
// FileBuffer, CombinedBuffer). The pool's own bookkeeping relies on that and
// takes no lock; idle is atomic because the workers update it.
type messageWorkers struct {
	limit  int
	shared chan hermod.Message
	// keyed[i] is worker i's private queue, nil until worker i starts.
	keyed   []chan hermod.Message
	started int
	// next is where the search for an unstarted worker resumes, so growing
	// the pool is linear in the limit overall rather than per worker.
	next int
	// idle counts workers waiting for a message right now.
	idle atomic.Int32

	spawn  func(work func())
	handle func(hermod.Message)
}

// newMessageWorkers returns a pool of at most limit workers. spawn runs a
// worker (the runner passes its WaitGroup's Go); handle processes a message.
func newMessageWorkers(limit int, spawn func(work func()), handle func(hermod.Message)) *messageWorkers {
	limit = max(limit, 1)
	return &messageWorkers{
		limit:  limit,
		shared: make(chan hermod.Message, limit),
		keyed:  make([]chan hermod.Message, limit),
		spawn:  spawn,
		handle: handle,
	}
}

// queueFor returns the queue m must be sent on, starting the worker that will
// take it from there if one is needed.
func (w *messageWorkers) queueFor(m hermod.Message) chan<- hermod.Message {
	if idx := keyedWorkerFor(m, w.limit); idx >= 0 {
		w.ensure(idx)
		return w.keyed[idx]
	}
	if w.idle.Load() == 0 && w.started < w.limit {
		w.ensure(w.nextUnstarted())
	}
	return w.shared
}

// ensure starts worker idx unless it is already running.
func (w *messageWorkers) ensure(idx int) {
	if w.keyed[idx] != nil {
		return
	}
	own := make(chan hermod.Message, keyedQueueDepth)
	w.keyed[idx] = own
	w.started++
	w.spawn(func() { w.work(own) })
}

// nextUnstarted returns the lowest-numbered worker from next onwards that has
// not started. The caller has checked that one exists.
func (w *messageWorkers) nextUnstarted() int {
	for w.keyed[w.next] != nil {
		w.next++
	}
	return w.next
}

// work is one worker: it serves its own keyed queue and the shared one until
// both are closed.
//
// A worker leaves when its queues are closed, not when the engine's context is
// cancelled. The dispatcher races that cancellation and can queue a message
// after it; a worker that had already left would never count it out of
// inFlightWg, and Start waits on that before closing the sink channels, so the
// engine never stopped. Consume returns once the context is cancelled and the
// queues are closed right after, so the workers still leave promptly, having
// handed on everything that was queued.
func (w *messageWorkers) work(own chan hermod.Message) {
	mine, pool := own, w.shared
	for mine != nil || pool != nil {
		var m hermod.Message
		var ok bool
		w.idle.Add(1)
		select {
		case m, ok = <-mine:
			w.idle.Add(-1)
			if !ok {
				// A closed channel is always ready; nil never is.
				mine = nil
				continue
			}
		case m, ok = <-pool:
			w.idle.Add(-1)
			if !ok {
				pool = nil
				continue
			}
		}
		w.handle(m)
	}
}

// close closes every queue, which lets each running worker finish what is
// queued and leave. Nothing may be queued after it.
func (w *messageWorkers) close() {
	close(w.shared)
	for _, q := range w.keyed {
		if q != nil {
			close(q)
		}
	}
}
