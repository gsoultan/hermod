// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package advisor

import (
	"sync"
	"time"
)

// SampleInterval is how often a running flow is read. A flow's numbers move
// on the scale of minutes; reading more often adds memory, not insight.
const SampleInterval = 30 * time.Second

// DefaultCapacity is how many samples a flow's window holds. Taken every
// SampleInterval, that is the last two hours.
const DefaultCapacity = 240

// Advisor keeps each running flow's recent samples.
//
// Memory is bounded twice: by capacity per flow, and by forgetting a flow
// when its engine stops. A sample holds counters only, a few hundred bytes
// for a flow of ten steps.
type Advisor struct {
	mu       sync.Mutex
	capacity int
	windows  map[string]*ring
}

// New returns an advisor that keeps capacity samples per flow.
func New(capacity int) *Advisor {
	return &Advisor{capacity: max(capacity, 2), windows: map[string]*ring{}}
}

// Observe adds a flow's newest sample, dropping its oldest when full.
func (a *Advisor) Observe(workflowID string, s Sample) {
	a.mu.Lock()
	defer a.mu.Unlock()
	w, ok := a.windows[workflowID]
	if !ok {
		w = &ring{buf: make([]Sample, a.capacity)}
		a.windows[workflowID] = w
	}
	w.add(s)
}

// Retain drops the window of every flow not in running: their engines have
// stopped.
func (a *Advisor) Retain(running map[string]bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for id := range a.windows {
		if !running[id] {
			delete(a.windows, id)
		}
	}
}

// Forget drops a flow's window; its engine has stopped.
func (a *Advisor) Forget(workflowID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.windows, workflowID)
}

// Flows is how many flows have a window.
func (a *Advisor) Flows() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.windows)
}

// Samples returns a copy of a flow's window, oldest first.
func (a *Advisor) Samples(workflowID string) []Sample {
	a.mu.Lock()
	defer a.mu.Unlock()
	w, ok := a.windows[workflowID]
	if !ok {
		return nil
	}
	return w.ordered()
}

// Report analyses a flow's window.
func (a *Advisor) Report(flow Flow) Report {
	return Analyze(a.Samples(flow.ID), flow)
}

// ring is a fixed-size buffer of samples.
type ring struct {
	buf  []Sample
	next int
	full bool
}

func (r *ring) add(s Sample) {
	r.buf[r.next] = s
	r.next = (r.next + 1) % len(r.buf)
	if r.next == 0 {
		r.full = true
	}
}

// ordered copies the samples out, oldest first. The maps inside a sample are
// shared, which is safe: nothing writes to a sample's maps once it is added.
func (r *ring) ordered() []Sample {
	if !r.full {
		return append([]Sample(nil), r.buf[:r.next]...)
	}
	out := make([]Sample, 0, len(r.buf))
	out = append(out, r.buf[r.next:]...)
	return append(out, r.buf[:r.next]...)
}
