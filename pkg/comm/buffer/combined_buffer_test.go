// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package buffer

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

func TestCombinedBuffer_ProduceConsume(t *testing.T) {
	dir := filepath.Join(os.TempDir(), "hermod-buffer-test")
	_ = os.RemoveAll(dir)
	cb, err := NewCombinedBuffer(8, dir, 0, &CombinedOptions{ProduceTimeout: 1 * time.Millisecond})
	if err != nil {
		t.Fatalf("new combined buffer: %v", err)
	}
	defer cb.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	// Produce more than ring capacity to force spill to file
	const N = 32
	for i := range N {
		m := message.AcquireMessage()
		m.SetID(strconv.Itoa(i))
		if err := cb.Produce(ctx, m); err != nil {
			t.Fatalf("produce %d: %v", i, err)
		}
	}

	var consumed atomic.Int64
	go func() {
		_ = cb.Consume(ctx, func(_ context.Context, _m hermod.Message) error {
			consumed.Add(1)
			return nil
		})
	}()

	// Wait for consumption
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if consumed.Load() >= N {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if consumed.Load() < N {
		t.Fatalf("expected to consume %d, got %d", N, consumed.Load())
	}
}
