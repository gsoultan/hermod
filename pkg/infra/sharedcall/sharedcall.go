// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

// Package sharedcall runs one call per key for every concurrent caller asking
// for it, and keeps that call alive for as long as any of them is still
// waiting.
//
// singleflight alone runs the call on the context of whichever caller happens
// to start it, and hands that one result to everyone. So the starter's
// cancellation -- its run stopped, a rerun replaced it, its batch ended -- comes
// back to every other caller as its own "context canceled" while their contexts
// are live. Under load, messages that build the same request then split into
// some that succeed and some that fail, and which ones fail moves around from
// run to run. Hermod hit this in api_lookup, panmail_providers, the registry's
// source and sink config reads, and the shared Postgres pooler.
package sharedcall

import (
	"context"
	"errors"
	"sync"

	"golang.org/x/sync/singleflight"
)

// Group is a set of shared calls keyed by string. The zero value is ready to
// use and must not be copied after first use.
type Group struct {
	sf singleflight.Group

	mu      sync.Mutex
	waiters map[string]*waiters
}

// waiters is the set of callers currently waiting on one key's call. The call
// runs on ctx, which cancel ends when count reaches zero.
type waiters struct {
	ctx    context.Context
	cancel context.CancelFunc
	count  int
}

// join registers ctx's caller as a waiter on key and returns the context the
// key's call runs on, and the leave to call when this caller stops waiting.
//
// The call's context carries the first waiter's values -- tracing, the
// registry -- without its cancellation: every caller of one key asks for the
// same thing, so their values agree on everything the call reads.
func (g *Group) join(ctx context.Context, key string) (callCtx context.Context, leave func()) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.waiters == nil {
		g.waiters = map[string]*waiters{}
	}
	w := g.waiters[key]
	if w == nil {
		w = &waiters{}
		w.ctx, w.cancel = context.WithCancel(context.WithoutCancel(ctx))
		g.waiters[key] = w
	}
	w.count++

	var once sync.Once
	return w.ctx, func() {
		once.Do(func() {
			g.mu.Lock()
			defer g.mu.Unlock()
			w.count--
			if w.count == 0 {
				w.cancel()
				if g.waiters[key] == w {
					delete(g.waiters, key)
				}
			}
		})
	}
}

// Do runs fn once for every concurrent caller asking for key and returns its
// result to each of them, unless the caller's own ctx ends first, in which case
// that caller gets its ctx.Err() and the others keep waiting. fn's context is
// cancelled only once the last waiter has left, so a call nobody waits for any
// more still ends.
func (g *Group) Do(ctx context.Context, key string, fn func(context.Context) (any, error)) (any, error) {
	// Two attempts, not one: the last waiter leaving cancels the call's
	// context, and until fn notices and returns, singleflight still hands that
	// in-flight call to a new arrival. The arrival would then be told
	// "context canceled" while live -- the very thing this package exists to
	// stop -- so a cancellation that is not the caller's own is retried once,
	// by which time fn has returned and the key is free.
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		callCtx, leave := g.join(ctx, key)
		ch := g.sf.DoChan(key, func() (any, error) { return fn(callCtx) })

		var val any
		var err error
		select {
		case r := <-ch:
			val, err = r.Val, r.Err
		case <-ctx.Done():
			leave()
			return nil, ctx.Err()
		}
		leave()

		if attempt == 0 && errors.Is(err, context.Canceled) && ctx.Err() == nil && callCtx.Err() != nil {
			continue
		}
		return val, err
	}
}
