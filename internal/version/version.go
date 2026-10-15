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
