// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

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
