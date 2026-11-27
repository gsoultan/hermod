// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package hermod

import "context"

// BusyWaiter waits out busy answers (ErrBusy) on behalf of a workflow step.
//
// A step such as a lookup cannot wait on its own: the engine running the
// workflow has to know, so that it shows the wait and its stall watchdog does
// not mistake waiting for being stuck. The engine puts itself in the context
// it runs a workflow on, and the step runner finds it there.
type BusyWaiter interface {
	// WaitOutBusy runs attempt until it answers anything other than busy, and
	// returns that answer, or the context's error if the context ends first.
	// name says what is waiting, such as "step:<node id>".
	WaitOutBusy(ctx context.Context, name string, attempt func() error) error
}

type busyWaiterKey struct{}

// WithBusyWaiter returns ctx carrying w.
func WithBusyWaiter(ctx context.Context, w BusyWaiter) context.Context {
	return context.WithValue(ctx, busyWaiterKey{}, w)
}

// BusyWaiterFrom returns the BusyWaiter ctx carries, if any. A preview or a
// test run has none, and a busy answer is then reported as it is.
func BusyWaiterFrom(ctx context.Context) (BusyWaiter, bool) {
	w, ok := ctx.Value(busyWaiterKey{}).(BusyWaiter)
	return w, ok && w != nil
}
