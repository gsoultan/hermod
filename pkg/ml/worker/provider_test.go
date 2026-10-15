// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package worker

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// The worker in force. It used to be read from the environment once, at
// start: connecting a worker meant setting two variables and restarting
// Hermod. A worker saved from the console now takes over at once, and the
// environment, when it names one, still wins.
// ---------------------------------------------------------------------------

func TestProviderPrefersTheEnvironmentsWorker(t *testing.T) {
	env := New("http://env:8090", "env-token", nil)
	p := NewProvider(func() *Client { return env })
	p.SetSaved(New("http://saved:8090", "saved-token", nil))

	c, src := p.Current()
	if c != env || src != SourceEnvironment {
		t.Fatalf("Current() = %+v, %q; want the environment's worker", c, src)
	}
}

func TestProviderAnswersTheSavedWorkerAtOnce(t *testing.T) {
	p := NewProvider(func() *Client { return nil })
	if c, src := p.Current(); c != nil || src != SourceNone {
		t.Fatalf("Current() = %+v, %q; want none", c, src)
	}

	saved := New("http://saved:8090", "tok", nil)
	p.SetSaved(saved)
	if c, src := p.Current(); c != saved || src != SourceSaved {
		t.Fatalf("after SetSaved, Current() = %+v, %q; want the saved worker", c, src)
	}

	p.SetSaved(nil)
	if c, src := p.Current(); c != nil || src != SourceNone {
		t.Fatalf("after clearing, Current() = %+v, %q; want none", c, src)
	}
}

func TestProviderReadsTheEnvironmentOnce(t *testing.T) {
	var reads atomic.Int32
	p := NewProvider(func() *Client { reads.Add(1); return nil })
	for range 3 {
		p.Current()
		p.Env()
	}
	if n := reads.Load(); n != 1 {
		t.Errorf("the environment was read %d times, want once", n)
	}
}

func TestProviderRefreshReadsTheSavedWorkerAtMostEveryInterval(t *testing.T) {
	now := time.Unix(1_000, 0)
	p := NewProvider(func() *Client { return nil })
	p.now = func() time.Time { return now }

	var loads atomic.Int32
	load := func(context.Context) (*Client, error) {
		loads.Add(1)
		return New("http://saved:8090", "", nil), nil
	}
	ctx := t.Context()
	if err := p.Refresh(ctx, time.Minute, load); err != nil {
		t.Fatal(err)
	}
	if c, src := p.Current(); c == nil || src != SourceSaved {
		t.Fatalf("after the first refresh, Current() = %+v, %q; want the saved worker", c, src)
	}
	_ = p.Refresh(ctx, time.Minute, load)
	if n := loads.Load(); n != 1 {
		t.Fatalf("loaded %d times within the interval, want 1", n)
	}
	now = now.Add(time.Minute)
	_ = p.Refresh(ctx, time.Minute, load)
	if n := loads.Load(); n != 2 {
		t.Fatalf("loaded %d times after the interval, want 2", n)
	}
}

func TestProviderRefreshKeepsTheWorkerWhenTheReadFails(t *testing.T) {
	p := NewProvider(func() *Client { return nil })
	saved := New("http://saved:8090", "", nil)
	p.SetSaved(saved)
	p.now = func() time.Time { return time.Now().Add(time.Hour) }

	err := p.Refresh(t.Context(), time.Minute, func(context.Context) (*Client, error) {
		return nil, errors.New("database is down")
	})
	if err == nil {
		t.Fatal("Refresh hid the failed read")
	}
	if c, _ := p.Current(); c != saved {
		t.Errorf("a failed read dropped the worker: Current() = %+v", c)
	}
}

func TestProviderRefreshDoesNotReadWhenTheEnvironmentNamesAWorker(t *testing.T) {
	p := NewProvider(func() *Client { return New("http://env:8090", "", nil) })
	err := p.Refresh(t.Context(), time.Minute, func(context.Context) (*Client, error) {
		t.Error("read the saved worker although the environment's wins")
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSetSavedCountsAsARead(t *testing.T) {
	p := NewProvider(func() *Client { return nil })
	p.SetSaved(New("http://saved:8090", "", nil))
	_ = p.Refresh(t.Context(), time.Minute, func(context.Context) (*Client, error) {
		t.Error("re-read the saved worker right after it was set")
		return nil, nil
	})
}

// Every prediction, training and dataset call asks for the worker; one that
// is being saved at the same time must not race them. Run with -race.
func TestProviderIsSafeForConcurrentUse(t *testing.T) {
	p := NewProvider(func() *Client { return nil })
	p.now = time.Now
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			for j := range 200 {
				switch (i + j) % 3 {
				case 0:
					p.SetSaved(New("http://saved:8090", "", nil))
				case 1:
					p.Current()
				default:
					_ = p.Refresh(context.Background(), 0, func(context.Context) (*Client, error) {
						return New("http://other:8090", "", nil), nil
					})
				}
			}
		})
	}
	wg.Wait()
}

func TestConfigureTrimsTheURLAndReadsTheTrainTimeout(t *testing.T) {
	t.Setenv("HERMOD_ML_TRAIN_TIMEOUT", "5m")
	c := Configure(" http://ml:8090/ ", "tok")
	if c.URL != "http://ml:8090" || c.Token != "tok" || c.TrainTimeout != 5*time.Minute {
		t.Errorf("Configure = %+v", c)
	}
}
