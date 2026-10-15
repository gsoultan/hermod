// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package api

import (
	"testing"
)

// TestNewServerStartsSessionRevocation. The revoker is only half a feature if
// nothing refreshes it: revocations never reach other instances and the list
// never shrinks. This asserts the real construction path starts it, because
// that is the part no unit test of the Revoker itself can catch.
func TestNewServerStartsSessionRevocation(t *testing.T) {
	s := NewServer(nil, nil, nil, "", nil)

	if !s.Handler.RevocationRefreshRunning() {
		t.Fatal("NewServer did not start the session revocation refresher; " +
			"revocations would never replicate and the list would never be pruned")
	}

	s.Stop()
	if s.Handler.RevocationRefreshRunning() {
		t.Error("Stop left the revocation refresher running")
	}
}
