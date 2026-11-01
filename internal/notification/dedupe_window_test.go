// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package notification

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
)

// lastSent keyed every alert by workflow ID and title and never removed an
// entry. The window it enforces is five minutes, so anything older is dead
// weight -- and a deployment that creates and deletes workflows (every CI run
// does) grew the map for the life of the process.
func TestNotifyForgetsDedupeEntriesPastTheWindow(t *testing.T) {
	svc := NewService(&stubStorage{})
	svc.SetLogger(&recordLogger{})
	now := time.Now()
	svc.now = func() time.Time { return now }

	for i := range 3000 {
		svc.Notify(context.Background(), "Workflow Error", "m", storage.Workflow{ID: fmt.Sprintf("w%d", i)})
	}
	svc.Wait()

	// Past the window every one of those entries is dead weight; one more alert
	// is the moment they should go.
	now = now.Add(dedupeWindow + time.Second)
	svc.Notify(context.Background(), "Workflow Error", "m", storage.Workflow{ID: "fresh"})
	svc.Wait()

	svc.mu.Lock()
	kept := len(svc.lastSent)
	svc.mu.Unlock()
	if kept != 1 {
		t.Errorf("lastSent holds %d entries after the window passed, want 1 (the fresh alert)", kept)
	}
}

// The sweep must not shorten the window: an alert inside it is still a repeat.
func TestNotifyStillDedupesAcrossTheSweep(t *testing.T) {
	svc := NewService(&stubStorage{})
	svc.SetLogger(&recordLogger{})
	p := &recordingProvider{}
	svc.AddProvider(p)
	now := time.Now()
	svc.now = func() time.Time { return now }

	svc.Notify(context.Background(), "Workflow Error", "m", storage.Workflow{ID: "w1"})
	now = now.Add(dedupeWindow - time.Second)
	for i := range dedupeSweepEvery + 1 {
		svc.Notify(context.Background(), "Workflow Error", "m", storage.Workflow{ID: fmt.Sprintf("other%d", i)})
	}
	svc.Notify(context.Background(), "Workflow Error", "m", storage.Workflow{ID: "w1"})
	svc.Wait()

	if got := p.count(); got != dedupeSweepEvery+2 {
		t.Errorf("sends = %d, want %d: w1 inside its window must still be deduped", got, dedupeSweepEvery+2)
	}
}
