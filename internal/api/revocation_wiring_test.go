// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

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
