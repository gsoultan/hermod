// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

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
