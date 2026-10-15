// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

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
