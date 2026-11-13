// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package tracing

import (
	"context"
	"testing"
)

// Extract runs at the read, at every node and at every sink write; Inject at
// the read. With no TracerProvider installed — the default — no message
// carries a traceparent and no span context is valid, so both have nothing
// to do. They must find that out without allocating: three to four calls per
// message added up to ~14% of every allocation in BenchmarkWorkflowThroughput.
func TestUntracedMessagesCostNoAllocations(t *testing.T) {
	m := msgWithMetadata(t, 4)
	ctx := context.Background()

	if n := testing.AllocsPerRun(1000, func() { _ = Extract(ctx, m) }); n != 0 {
		t.Errorf("Extract on a message with no traceparent: %.1f allocations, want 0", n)
	}
	if n := testing.AllocsPerRun(1000, func() { Inject(ctx, m) }); n != 0 {
		t.Errorf("Inject with no span in the context: %.1f allocations, want 0", n)
	}
}
