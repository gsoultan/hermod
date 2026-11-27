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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// startedWorkers runs a messageWorkers whose workers call handle, and counts
// how many workers it started.
func startedWorkers(t *testing.T, limit int, handle func(hermod.Message)) (*messageWorkers, *atomic.Int32, *sync.WaitGroup) {
	t.Helper()
	var wg sync.WaitGroup
	var started atomic.Int32
	w := newMessageWorkers(limit, func(work func()) {
		started.Add(1)
		wg.Go(work)
	}, handle)
	return w, &started, &wg
}

func unkeyed() hermod.Message { return message.AcquireMessage() }

func keyedMsg(key string) hermod.Message {
	m := message.AcquireMessage()
	m.SetMetadata(hermod.MetaOrderingKey, key)
	return m
}

// Every engine used to start its full pool of message workers -- 128 by
// default -- as it started, whether or not a message ever arrived. Measured,
// that was 128 of the 139 goroutines an idle workflow cost
// (internal/test/scale). A workflow that has received nothing must hold none.
func TestMessageWorkersStartNoneUntilAMessageArrives(t *testing.T) {
	w, started, wg := startedWorkers(t, 128, func(hermod.Message) {})
	w.close()
	wg.Wait()
	if n := started.Load(); n != 0 {
		t.Fatalf("started %d workers with no messages, want 0", n)
	}
}

// A trickle of messages is served by the worker already waiting, not by a new
// one per message.
func TestMessageWorkersReuseAnIdleWorker(t *testing.T) {
	done := make(chan struct{}, 16)
	w, started, wg := startedWorkers(t, 128, func(hermod.Message) { done <- struct{}{} })
	for range 10 {
		send(w, unkeyed())
		<-done
		waitIdle(t, w, 1)
	}
	w.close()
	wg.Wait()
	if n := started.Load(); n != 1 {
		t.Fatalf("10 messages one after another started %d workers, want 1", n)
	}
}

// Work that arrives while every worker is busy gets another worker, up to the
// limit and no further.
func TestMessageWorkersGrowUnderLoadUpToTheLimit(t *testing.T) {
	release := make(chan struct{})
	var busy atomic.Int32
	w, started, wg := startedWorkers(t, 4, func(hermod.Message) {
		busy.Add(1)
		<-release
	})
	for range 4 {
		send(w, unkeyed())
		waitFor(t, func() bool { return busy.Load() == started.Load() })
	}
	// A fifth message finds every worker busy and the limit reached: it waits
	// in the shared queue rather than starting a fifth worker.
	send(w, unkeyed())
	if n := started.Load(); n != 4 {
		t.Fatalf("started %d workers, want the limit of 4", n)
	}
	close(release)
	w.close()
	wg.Wait()
	if n := busy.Load(); n != 5 {
		t.Fatalf("handled %d messages, want 5", n)
	}
}

// Messages with one ordering key go to one worker, in order, whichever worker
// that is and however many others are running.
func TestMessageWorkersKeepOrderPerKey(t *testing.T) {
	var mu sync.Mutex
	seen := map[string][]int{}
	w, _, wg := startedWorkers(t, 8, func(m hermod.Message) {
		key := hermod.OrderingKey(m)
		mu.Lock()
		seen[key] = append(seen[key], m.Data()["n"].(int))
		mu.Unlock()
	})
	keys := []string{"a", "b", "c", "d", "e"}
	for n := range 200 {
		for _, k := range keys {
			m := keyedMsg(k)
			m.SetData("n", n)
			w.queueFor(m) <- m
		}
	}
	w.close()
	wg.Wait()
	for _, k := range keys {
		got := seen[k]
		if len(got) != 200 {
			t.Fatalf("key %s: %d messages, want 200", k, len(got))
		}
		for i, n := range got {
			if n != i {
				t.Fatalf("key %s: message %d arrived at position %d", k, n, i)
			}
		}
	}
}

// A keyed message starts the worker that owns its key, and only that one.
func TestMessageWorkersStartOnlyTheKeysOwner(t *testing.T) {
	done := make(chan struct{}, 4)
	w, started, wg := startedWorkers(t, 128, func(hermod.Message) { done <- struct{}{} })
	m := keyedMsg("row-42")
	w.queueFor(m) <- m
	<-done
	w.close()
	wg.Wait()
	if n := started.Load(); n != 1 {
		t.Fatalf("one keyed message started %d workers, want 1", n)
	}
}

func send(w *messageWorkers, m hermod.Message) { w.queueFor(m) <- m }

func waitIdle(t *testing.T, w *messageWorkers, n int32) {
	t.Helper()
	waitFor(t, func() bool { return w.idle.Load() == n })
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached within 5s")
		}
		time.Sleep(time.Millisecond)
	}
}
