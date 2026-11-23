// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package cron

import (
	"context"
	"testing"
	"time"
)

// Every firing used to carry the same id, "cron_<entry>", and the entry is 1
// for the only schedule a source has. Traces and runs are keyed by message id,
// so every run a cron workflow ever made was one trace: a "cron_1" run hours
// long with thousands of steps, failed for good by the first error in any of
// them. A successful run could never show as one.
func TestEachFiringIsItsOwnMessage(t *testing.T) {
	src := NewCronSource("@every 1s", `{"payload":""}`)
	defer src.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	first, err := src.Read(ctx)
	if err != nil {
		t.Fatalf("first firing: %v", err)
	}
	second, err := src.Read(ctx)
	if err != nil {
		t.Fatalf("second firing: %v", err)
	}
	if first.ID() == second.ID() {
		t.Fatalf("two firings share the id %q, so their traces merge into one run", first.ID())
	}
	for _, id := range []string{first.ID(), second.ID()} {
		if len(id) < len("cron_") || id[:5] != "cron_" {
			t.Errorf("id %q no longer says it came from a schedule", id)
		}
	}
}
