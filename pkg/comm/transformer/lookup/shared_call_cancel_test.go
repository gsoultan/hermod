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
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// ---------------------------------------------------------------------------
// One message's cancellation must not fail another message's lookup.
//
// Identical requests share one call (see package sharedcall). The call used to run
// on the context of whichever message started it, so when that message was
// cancelled -- its run stopped, a rerun replaced it -- every other message
// waiting on the same request failed with that message's "context canceled".
// Rows of one batch that build the same request (same user, same entity) then
// split into some that succeed and some that fail, run after run.
// ---------------------------------------------------------------------------

// blockingServer answers only once release is closed, and reports each request
// it receives on arrived.
func blockingServer(t *testing.T, reply string) (srv *httptest.Server, arrived chan struct{}, release chan struct{}, calls *atomic.Int32) {
	t.Helper()
	arrived = make(chan struct{}, 8)
	release = make(chan struct{})
	calls = &atomic.Int32{}
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		arrived <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	return srv, arrived, release, calls
}

// waitOrFail waits for one value on ch.
func waitOrFail(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

type lookupResult struct {
	data map[string]any
	err  error
}

func TestAPILookupSharedCallSurvivesTheLeadersCancellation(t *testing.T) {
	srv, arrived, release, calls := blockingServer(t, `{"token":"t-1"}`)
	tr, reg := newAPIFixture()
	cfg := map[string]any{
		"method":       "POST",
		"url":          srv.URL + "/share-sessions",
		"body":         `{"user_id":"{{.user_id}}"}`,
		"responsePath": "token",
		"targetField":  "session",
	}

	run := func(ctx context.Context) <-chan lookupResult {
		out := make(chan lookupResult, 1)
		go func() {
			msg := message.AcquireMessage()
			defer msg.Release()
			msg.SetData("user_id", "u-1")
			res, err := tr.Transform(context.WithValue(ctx, hermod.RegistryKey, reg), msg, cfg)
			var data map[string]any
			if res != nil {
				data = res.Data()
			}
			out <- lookupResult{data: data, err: err}
		}()
		return out
	}

	leaderCtx, cancelLeader := context.WithCancel(t.Context())
	defer cancelLeader()
	leader := run(leaderCtx)
	waitOrFail(t, arrived, "the leader's request")

	follower := run(t.Context())
	// Give the follower time to join the in-flight call. There is no hook to
	// observe a singleflight join; if it has not joined yet it makes its own
	// call, and the test still holds it to succeeding.
	time.Sleep(100 * time.Millisecond)

	cancelLeader()
	if r := <-leader; !errors.Is(r.err, context.Canceled) {
		t.Errorf("the cancelled message got err=%v, want its own context.Canceled", r.err)
	}
	close(release)

	r := <-follower
	if r.err != nil {
		t.Fatalf("a message whose own context was live failed with %v: "+
			"another message's cancellation was reported as its error", r.err)
	}
	if got := r.data["session"]; got != "t-1" {
		t.Errorf("session = %v, want t-1", got)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("the endpoint was called %d times, want 1: identical concurrent requests are meant to share one call", n)
	}
}

func TestPanmailProvidersSharedCallSurvivesTheLeadersCancellation(t *testing.T) {
	srv, arrived, release, calls := blockingServer(t, twoProviders)
	tr, reg := newPanmailProvidersFixture()
	cfg := map[string]any{
		"baseUrl":     srv.URL,
		"apiKey":      "k",
		"targetField": "provider",
	}

	run := func(ctx context.Context) <-chan lookupResult {
		out := make(chan lookupResult, 1)
		go func() {
			msg := message.AcquireMessage()
			defer msg.Release()
			res, err := tr.Transform(context.WithValue(ctx, hermod.RegistryKey, reg), msg, cfg)
			var data map[string]any
			if res != nil {
				data = res.Data()
			}
			out <- lookupResult{data: data, err: err}
		}()
		return out
	}

	leaderCtx, cancelLeader := context.WithCancel(t.Context())
	defer cancelLeader()
	leader := run(leaderCtx)
	waitOrFail(t, arrived, "the leader's request")

	follower := run(t.Context())
	time.Sleep(100 * time.Millisecond)

	cancelLeader()
	if r := <-leader; !errors.Is(r.err, context.Canceled) {
		t.Errorf("the cancelled message got err=%v, want its own context.Canceled", r.err)
	}
	close(release)

	r := <-follower
	if r.err != nil {
		t.Fatalf("a message whose own context was live failed with %v: "+
			"another message's cancellation was reported as its error", r.err)
	}
	if r.data["provider"] == nil {
		t.Error("provider was not set")
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("the gateway was called %d times, want 1", n)
	}
}

// The other half of the contract: a call nobody is waiting for any more must
// end, or stopping a workflow would leave its lookups running until their own
// timeouts. Here the only waiter is cancelled, so the endpoint must see the
// request cancelled too.
func TestAPILookupSharedCallEndsWhenTheLastWaiterLeaves(t *testing.T) {
	cancelled := make(chan struct{}, 1)
	arrived := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		arrived <- struct{}{}
		<-r.Context().Done()
		cancelled <- struct{}{}
	}))
	t.Cleanup(srv.Close)
	tr, reg := newAPIFixture()
	cfg := map[string]any{
		"method":      "GET",
		"url":         srv.URL + "/c/1",
		"targetField": "tier",
		"timeout":     "1h",
	}

	ctx, cancel := context.WithCancel(context.WithValue(t.Context(), hermod.RegistryKey, reg))
	done := make(chan error, 1)
	go func() {
		msg := message.AcquireMessage()
		defer msg.Release()
		_, err := tr.Transform(ctx, msg, cfg)
		done <- err
	}()
	waitOrFail(t, arrived, "the request")
	cancel()

	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	waitOrFail(t, cancelled, "the endpoint to see the request cancelled once its only waiter left")
}
