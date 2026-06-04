package version

import (
	"runtime/debug"
	"strings"
	"testing"
)

func TestVersionFormatting(t *testing.T) {
	if !strings.Contains(Version, "v1.2.1") && !strings.Contains(Version, "dev") {
		if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
			// If we have build info, it should match that
			if !strings.Contains(Version, info.Main.Version) {
				t.Errorf("Version %q does not contain expected build info version %q", Version, info.Main.Version)
			}
		} else {
			// Otherwise it should be our hardcoded default or newer
			// This is a loose check but ensures we don't regress to v1.1.0
			if strings.Contains(Version, "v1.1.0") {
				t.Errorf("Version %q still contains outdated v1.1.0", Version)
			}
		}
	}

	if !strings.Contains(Version, "Enterprise Edition") {
		t.Errorf("Version %q missing 'Enterprise Edition' suffix", Version)
	}
}
