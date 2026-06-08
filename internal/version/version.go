package version

import (
	"runtime/debug"
	"strings"
)

// Version is the current version of Hermod.
// It is set at build time via ldflags.
//
//go:generate ../../scripts/update-version.sh
var Version = "v1.2.3 (Enterprise Edition)"

func init() {
	// If the version is still the default (possibly outdated in source) or "dev",
	// try to use the more accurate version from build info if available.
	if info, ok := debug.ReadBuildInfo(); ok {
		if info.Main.Version != "" && info.Main.Version != "(devel)" {
			Version = info.Main.Version
			if !strings.Contains(Version, "Enterprise Edition") {
				Version += " (Enterprise Edition)"
			}
		}
	}
}
