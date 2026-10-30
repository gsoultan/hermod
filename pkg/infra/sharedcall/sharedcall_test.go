// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package sharedcall

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// blockingFn is a call that reports when it starts, waits for release or its
// context, and counts how often it ran.
type blockingFn struct {
	started   chan struct{}
	release   chan struct{}
	cancelled chan struct{}
	runs      atomic.Int32
}

func newBlockingFn() *blockingFn {
	return &blockingFn{
		started:   make(chan struct{}, 8),
		release:   make(chan struct{}),
		cancelled: make(chan struct{}, 8),
	}
}

func (b *blockingFn) call(ctx context.Context) (any, error) {
	b.runs.Add(1)
	b.started <- struct{}{}
	select {
	case <-b.release:
		return "value", nil
	case <-ctx.Done():
		b.cancelled <- struct{}{}
		return nil, ctx.Err()
	}
}

func wait(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

type result struct {
	val any
	err error
}

func start(g *Group, ctx context.Context, fn func(context.Context) (any, error)) <-chan result {
	out := make(chan result, 1)
	go func() {
		v, err := g.Do(ctx, "k", fn)
		out <- result{v, err}
	}()
	return out
}

// The starter is cancelled while another caller waits on the same key: the
// starter gets its own cancellation, the other caller gets the value, and the
// call ran once.
func TestDoSurvivesTheStartersCancellation(t *testing.T) {
	g := &Group{}
	fn := newBlockingFn()

	leaderCtx, cancelLeader := context.WithCancel(t.Context())
	defer cancelLeader()
	leader := start(g, leaderCtx, fn.call)
	wait(t, fn.started, "the call to start")

	follower := start(g, t.Context(), fn.call)
	// There is no hook to observe a singleflight join; give it a moment.
	time.Sleep(50 * time.Millisecond)

	cancelLeader()
	if r := <-leader; !errors.Is(r.err, context.Canceled) {
		t.Errorf("leader err = %v, want context.Canceled", r.err)
	}
	select {
	case <-fn.cancelled:
		t.Fatal("the call was cancelled while a caller was still waiting on it")
	case <-time.After(50 * time.Millisecond):
	}
	close(fn.release)

	r := <-follower
	if r.err != nil {
		t.Fatalf("follower err = %v: another caller's cancellation was reported as its error", r.err)
	}
	if r.val != "value" {
		t.Errorf("follower val = %v, want value", r.val)
	}
	if n := fn.runs.Load(); n != 1 {
		t.Errorf("the call ran %d times, want 1", n)
	}
}

// The only caller is cancelled: the call's context must end too, or a stopped
// workflow would leave its calls running until their own timeouts.
func TestDoEndsTheCallWhenTheLastWaiterLeaves(t *testing.T) {
	g := &Group{}
	fn := newBlockingFn()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	only := start(g, ctx, fn.call)
	wait(t, fn.started, "the call to start")

	cancel()
	if r := <-only; !errors.Is(r.err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", r.err)
	}
	wait(t, fn.cancelled, "the call to see its context cancelled once its only waiter left")
}

// A caller arriving while the previous, abandoned call is still winding down
// must not inherit its cancellation: it gets a fresh call.
func TestDoRetriesACancellationThatIsNotTheCallersOwn(t *testing.T) {
	g := &Group{}
	// The first run blocks until its context ends and takes a moment to
	// return, so the second caller can join it after the last waiter left.
	var runs atomic.Int32
	slowExit := make(chan struct{})
	fn := func(ctx context.Context) (any, error) {
		if runs.Add(1) == 1 {
			<-ctx.Done()
			<-slowExit
			return nil, ctx.Err()
		}
		return "fresh", nil
	}

	ctx, cancel := context.WithCancel(t.Context())
	first := start(g, ctx, fn)
	time.Sleep(50 * time.Millisecond)
	cancel()
	if r := <-first; !errors.Is(r.err, context.Canceled) {
		t.Fatalf("first err = %v, want context.Canceled", r.err)
	}

	// The first call's context is cancelled but fn has not returned, so
	// singleflight still holds the key.
	second := start(g, t.Context(), fn)
	time.Sleep(50 * time.Millisecond)
	close(slowExit)

	r := <-second
	if r.err != nil {
		t.Fatalf("second err = %v, want a fresh call", r.err)
	}
	if r.val != "fresh" {
		t.Errorf("second val = %v, want fresh", r.val)
	}
	if n := runs.Load(); n != 2 {
		t.Errorf("fn ran %d times, want 2", n)
	}
}

// A caller whose context is already over does not start or join anything.
func TestDoRefusesAnEndedContext(t *testing.T) {
	g := &Group{}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var ran atomic.Bool
	_, err := g.Do(ctx, "k", func(context.Context) (any, error) { ran.Store(true); return nil, nil })
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if ran.Load() {
		t.Error("the call ran for a caller that had already left")
	}
}
