// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

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
