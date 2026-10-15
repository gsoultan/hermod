// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

package sse

import (
	"context"
	"sync"
	"testing"
	"time"
)

func newTestHub() *Hub {
	return &Hub{
		subs:     make(map[string]map[chan Event]*sync.Once),
		configs:  make(map[string]StreamConfig),
		shutdown: make(chan struct{}),
	}
}

// TestShutdownThenUnsubNoDoubleClose verifies that calling Shutdown (which closes
// all subscriber channels) followed by the subscriber's unsub function does not
// panic with "close of closed channel".
func TestShutdownThenUnsubNoDoubleClose(t *testing.T) {
	h := newTestHub()
	_, unsub := h.Subscribe("topic", 4)

	h.Shutdown(t.Context())

	// Previously this panicked because unsub unconditionally closed an
	// already-closed channel.
	unsub()
}

// TestDoubleUnsubNoPanic verifies unsub is idempotent.
func TestDoubleUnsubNoPanic(t *testing.T) {
	h := newTestHub()
	_, unsub := h.Subscribe("topic", 4)
	unsub()
	unsub()
}

// TestConcurrentShutdownAndUnsub stresses the close path under the race detector.
func TestConcurrentShutdownAndUnsub(t *testing.T) {
	h := newTestHub()

	var wg sync.WaitGroup
	for range 50 {
		_, unsub := h.Subscribe("topic", 4)
		wg.Go(func() {
			unsub()
		})
	}
	wg.Go(func() {
		h.Shutdown(t.Context())
	})
	wg.Wait()
}

// TestPublishAfterUnsubNoSendOnClosed verifies publishing while subscribers come
// and go does not send on a closed channel.
func TestPublishAfterUnsubNoSendOnClosed(t *testing.T) {
	h := newTestHub()
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()

	var wg sync.WaitGroup
	wg.Go(func() {
		for ctx.Err() == nil {
			h.Publish("topic", Event{Data: []byte("x")})
		}
	})
	for range 100 {
		_, unsub := h.Subscribe("topic", 1)
		unsub()
	}
	cancel()
	wg.Wait()
}
