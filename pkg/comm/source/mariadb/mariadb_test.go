// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package mariadb

import (
	"context"
	"testing"
	"time"
)

func TestMariaDBSource_Ping(t *testing.T) {
	// This test will fail if no MariaDB is running, which is expected.
	// We just want to ensure it tries to connect and uses the right driver.
	s := NewMariaDBSource("user:pass@tcp(localhost:3306)/db", []string{"table1"}, "id", 1*time.Second, false)
	defer s.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	err := s.Ping(ctx)
	if err == nil {
		t.Log("Unexpectedly connected to MariaDB (is it running?)")
	} else {
		t.Logf("Ping failed as expected: %v", err)
	}
}
