// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package lookup

import (
	"context"
	"sync"

	"golang.org/x/sync/singleflight"
)

// sharedCalls runs one call per key for every concurrent caller asking for it,
// and keeps that call alive for as long as any of them is still waiting.
//
// singleflight alone ran the call on the context of whichever caller happened
// to start it, and handed that one result to everyone. So the starter's
// cancellation -- its run stopped, a rerun replaced it, its batch ended -- came
// back to every other message as its own "context canceled" while their
// contexts were live. Under load, rows that build the same request (same user,
// same entity) then split into some that succeed and some that fail, and the
// failures move around from run to run.
//
// The call's context is instead cancelled only once the last waiter has left:
// a message that is cancelled returns its own ctx.Err() at once and leaves the
// call to the others, and stopping a workflow still cancels a call nobody is
// waiting for (TestStoppingAWorkflowCancelsAnInFlightNodeCall).
type sharedCalls struct {
	sf singleflight.Group

	mu      sync.Mutex
	waiters map[string]*sharedWaiters
}

// sharedWaiters is the set of callers currently waiting on one key's call.
// The call runs on ctx, which cancel ends when count reaches zero.
type sharedWaiters struct {
	ctx    context.Context
	cancel context.CancelFunc
	count  int
}

// join registers ctx's caller as a waiter on key and returns the context the
// key's call runs on, and the leave to call when this caller stops waiting.
//
// The call's context carries the first waiter's values -- tracing, the
// registry -- without its cancellation: every caller of one key built the
// same request from the same node, so their values agree on everything the
// call reads.
func (s *sharedCalls) join(ctx context.Context, key string) (callCtx context.Context, leave func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.waiters == nil {
		s.waiters = map[string]*sharedWaiters{}
	}
	w := s.waiters[key]
	if w == nil {
		w = &sharedWaiters{}
		w.ctx, w.cancel = context.WithCancel(context.WithoutCancel(ctx))
		s.waiters[key] = w
	}
	w.count++

	var once sync.Once
	return w.ctx, func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			w.count--
			if w.count == 0 {
				w.cancel()
				if s.waiters[key] == w {
					delete(s.waiters, key)
				}
			}
		})
	}
}

// Do runs fn once for every concurrent caller asking for key and returns its
// result to each of them, unless the caller's own ctx ends first, in which case
// that caller gets its ctx.Err() and the others keep waiting.
func (s *sharedCalls) Do(ctx context.Context, key string, fn func(context.Context) (any, error)) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	callCtx, leave := s.join(ctx, key)
	defer leave()

	ch := s.sf.DoChan(key, func() (any, error) { return fn(callCtx) })
	select {
	case r := <-ch:
		return r.Val, r.Err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
