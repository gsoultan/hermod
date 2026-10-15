// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package service

import (
	"context"
	"testing"
	"time"
)

// Manage runs the application inside a service wrapper whose Run() blocks until
// the service is told to stop. When the application itself finishes — a
// listener that could not bind, an unrecoverable startup error — nothing was
// telling the wrapper to stop, so the process stayed alive with no work left to
// do and no listener open: invisible to a port check, and never restarted by a
// supervisor because it never exited.
func TestManageReturnsWhenRunFuncReturns(t *testing.T) {
	done := make(chan error, 1)
	go func() {
		done <- Manage(Config{Name: "hermod-manage-test"}, "run", func(_ context.Context) {})
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Manage returned an error: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Manage never returned after the application finished; the process would hang forever")
	}
}

// The application's context must stay live while it is running: cancelling it
// early would tear down the very work the service exists to do.
func TestManageContextStaysLiveWhileRunning(t *testing.T) {
	cancelled := make(chan struct{}, 1)
	done := make(chan error, 1)

	go func() {
		done <- Manage(Config{Name: "hermod-manage-ctx-test"}, "run", func(ctx context.Context) {
			select {
			case <-ctx.Done():
				cancelled <- struct{}{}
			case <-time.After(300 * time.Millisecond):
			}
		})
	}()

	select {
	case <-cancelled:
		t.Fatal("application context was cancelled while the application was still running")
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("Manage never returned")
	}
}
