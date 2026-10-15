// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package version

import (
	"strings"
	"testing"
)

func TestVersionFormatting(t *testing.T) {
	// Version should never be empty
	if Version == "" {
		t.Fatal("Version is empty")
	}

	// Should contain "Enterprise Edition"
	if !strings.Contains(Version, "Enterprise Edition") {
		t.Errorf("Version %q missing 'Enterprise Edition' suffix", Version)
	}

	// If it's not "dev", it should probably start with 'v'
	if !strings.Contains(Version, "dev") && !strings.HasPrefix(Version, "v") {
		t.Logf("Note: Version %q does not start with 'v', which is unusual for release tags", Version)
	}
}
