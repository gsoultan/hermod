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
