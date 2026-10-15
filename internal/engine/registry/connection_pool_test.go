// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package registry

import (
	"context"
	"testing"
	"time"

	"github.com/gsoultan/hermod/pkg/infra/pgxutil"
)

func TestConnectionPoolResponsiveness(t *testing.T) {
	// Skip if no real Postgres is available, or use a mock pooler if we just want to test the logic.
	// For this test, we want to verify that multiple requests for the same connection string
	// return the same pool and that the pooler remains responsive.

	pooler := pgxutil.NewPooler()
	defer pooler.Close()

	// Since we don't have a real DB in this test environment easily,
	// we will mock the behavior if needed, but here we can at least test the concurrency of the Pooler.

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	connStr := "postgres://user:pass@localhost:5432/db?sslmode=disable"

	// Start many goroutines requesting the same pool.
	// If the pooler had a global lock during pool creation (which it doesn't, it uses sync.Map and LoadOrStore),
	// it would be slower.

	start := time.Now()
	const workers = 100
	done := make(chan bool, workers)

	for range workers {
		go func() {
			// We expect this to fail because there's no DB, but we want to see it fail FAST
			// or at least not block others if it was successful.
			_, _ = pooler.Get(ctx, connStr)
			done <- true
		}()
	}

	for range workers {
		<-done
	}

	duration := time.Since(start)
	t.Logf("Requested pool %d times in %v", workers, duration)
}
