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
