// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

package worker

import (
	"strconv"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
)

// TestResourceAwareSharding_SelfIncludedWhenHeartbeatStale guards against a
// stability regression: a healthy worker whose persisted heartbeat has lagged
// past the online window must still keep its share of workflows instead of
// being excluded from the online set (which previously caused it to stop ALL of
// its workflows while two or more peers were considered online).
func TestResourceAwareSharding_SelfIncludedWhenHeartbeatStale(t *testing.T) {
	stale := time.Now().Add(-10 * time.Minute)
	fresh := time.Now()
	ms := &mockShardingStorage{
		workers: []storage.Worker{
			{ID: "worker-1", CPUUsage: 0.1, MemoryUsage: 0.1, LastSeen: &stale},
			{ID: "worker-2", CPUUsage: 0.1, MemoryUsage: 0.1, LastSeen: &fresh},
			{ID: "worker-3", CPUUsage: 0.1, MemoryUsage: 0.1, LastSeen: &fresh},
		},
	}

	// worker-1 is alive but its DB heartbeat is stale; force a cache refresh.
	w1 := &Worker{
		workerGUID:      "worker-1",
		storage:         ms,
		leaseTTLSeconds: 30,
		workerCacheTTL:  time.Millisecond,
	}

	assigned := 0
	for i := range 100 {
		id := "wf-" + strconv.Itoa(i)
		if w1.isAssigned(id, "") {
			assigned++
		}
	}

	if assigned == 0 {
		t.Fatalf("worker with stale heartbeat abandoned all workflows; expected it to keep its share")
	}
}
