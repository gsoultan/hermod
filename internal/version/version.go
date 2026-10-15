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
	"runtime/debug"
	"strings"
)

// Version is the current version of Hermod.
// It is set at build time via ldflags.
var Version = "dev"

func init() {
	// If the version is still "dev", try to use the more accurate version from build info if available.
	if info, ok := debug.ReadBuildInfo(); ok {
		if info.Main.Version != "" && info.Main.Version != "(devel)" {
			if Version == "dev" {
				Version = info.Main.Version
			}
		}
	}

	if !strings.Contains(Version, "Enterprise Edition") {
		Version += " (Enterprise Edition)"
	}
}
