// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

package optimizer_test

import (
	"strings"
	"sync"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/optimizer"
	"github.com/gsoultan/hermod/pkg/engine"
	"github.com/gsoultan/hermod/pkg/engine/telemetry"
)

// Per-node counters are now real, so the gate sees a node failing more than
// half its messages. Diverting the whole workflow to the dead-letter sink on
// that signal is not a call the optimizer may make on its own: the counters
// are lifetime totals, so one bad hour would pin Safe Mode forever. The gate
// tells the operator instead.
func TestSelfCorrection_HighErrorRateNotifiesButDoesNotEnterSafeMode(t *testing.T) {
	var mu sync.Mutex
	var titles []string
	gate := optimizer.NewSelfCorrectionGate(&mockLogger{}, func(_, title, _ string) {
		mu.Lock()
		titles = append(titles, title)
		mu.Unlock()
	})
	eng := engine.NewEngine(&mockSource{}, []hermod.Sink{&mockSink{}}, nil)

	gate.Analyze("wf", eng, telemetry.StatusUpdate{
		NodeMetrics:      map[string]uint64{"n1": 200},
		NodeErrorMetrics: map[string]uint64{"n1": 150},
	})

	if eng.IsSafeMode() {
		t.Fatal("the gate put the engine in Safe Mode on its own")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(titles) != 1 || !strings.Contains(titles[0], "Safe Mode") {
		t.Fatalf("notifications = %v", titles)
	}
}
