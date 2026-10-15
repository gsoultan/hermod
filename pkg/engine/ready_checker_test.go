// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package engine

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/buffer"
)

type mockReadySource struct {
	hermod.Source
	pingCount    int
	isReadyCount int
	isReadyErr   error
	mu           sync.Mutex
}

func (m *mockReadySource) Ping(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pingCount++
	return nil
}

func (m *mockReadySource) IsReady(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.isReadyCount++
	return m.isReadyErr
}

func (m *mockReadySource) Read(ctx context.Context) (hermod.Message, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (m *mockReadySource) Close() error {
	return nil
}

func TestEngineUsesReadyChecker(t *testing.T) {
	src := &mockReadySource{isReadyErr: errors.New("not ready yet")}
	buf := buffer.NewRingBuffer(10)
	eng := NewEngine(src, nil, buf)
	eng.SetConfig(Config{
		ReconnectInterval: 10 * time.Millisecond,
		StatusInterval:    10 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go eng.Start(ctx)

	// Wait for a few attempts
	time.Sleep(100 * time.Millisecond)

	src.mu.Lock()
	if src.isReadyCount == 0 {
		t.Error("Expected IsReady to be called, but it wasn't")
	}
	if src.pingCount > 0 {
		t.Error("Expected Ping NOT to be called because IsReady is available")
	}
	src.mu.Unlock()

	// Now make it ready
	src.mu.Lock()
	src.isReadyErr = nil
	src.mu.Unlock()

	time.Sleep(50 * time.Millisecond)

	src.mu.Lock()
	finalCount := src.isReadyCount
	src.mu.Unlock()

	if finalCount == 0 {
		t.Error("Expected IsReady to have been called")
	}
}
