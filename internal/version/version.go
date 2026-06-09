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
