// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package oracle

import (
	"context"
	"testing"
	"time"
)

func TestOracleSource_Ping(t *testing.T) {
	s := NewOracleSource("oracle://user:pass@localhost:1521/xe", []string{"table1"}, "id", 1*time.Second, false)
	defer s.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	err := s.Ping(ctx)
	if err == nil {
		t.Log("Unexpectedly connected to Oracle")
	} else {
		t.Logf("Ping failed as expected: %v", err)
	}
}
