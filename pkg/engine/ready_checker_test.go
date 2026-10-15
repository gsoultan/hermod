// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

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
