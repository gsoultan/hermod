// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

//go:build integration

package state

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
)

// The Redis store against a real server.
//
// The contract suite was written to run against every implementation, and until
// now only SQLite ran it — so the clause that had already caused a bug (a
// missing key returns no value and no error) was verified for one store out of
// three. Redis maps redis.Nil to that, but reading the mapping is not running
// it, which is the distinction this whole exercise keeps turning on.
//
// CI already runs a Redis service, so this costs nothing to keep honest.
//
//	HERMOD_INTEGRATION=1 REDIS_ADDR=127.0.0.1:6379 \
//	go test -tags=integration ./pkg/infra/state/
func TestRedisStateStoreContract(t *testing.T) {
	addr := os.Getenv("REDIS_ADDR")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || addr == "" {
		t.Skip("integration: set HERMOD_INTEGRATION=1 and REDIS_ADDR to run")
	}

	RunStoreContract(t, "redis", func(t *testing.T) hermod.StateStore {
		t.Helper()
		// A prefix per test keeps runs from colliding on a shared server and
		// makes a leftover key traceable to what wrote it.
		prefix := fmt.Sprintf("hermod-test/%d/", time.Now().UnixNano())
		s := NewRedisStateStore(addr, "", 0, prefix, 0)

		if _, err := s.Get(context.Background(), "ping"); err != nil {
			t.Skipf("redis unreachable: %v", err)
		}
		return s
	})
}
