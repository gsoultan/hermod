package version_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The release build stamps the version with `-ldflags -X <path>.Version=...`,
// and the linker silently ignores an -X whose path matches no symbol. So when
// the module path moved, the Dockerfile kept a path that no longer existed and
// the build stayed green while every released container reported "dev" —
// nothing observes the difference until someone asks a running instance what
// version it is, which is usually during an incident.
//
// Both build paths are checked here rather than only the one that broke,
// because the failure is silent in either.
func TestLdflagsPathMatchesModulePath(t *testing.T) {
	root := repoRoot(t)

	modulePath := modulePathFrom(t, filepath.Join(root, "go.mod"))
	want := modulePath + "/internal/version.Version"

	// -X takes <importpath>.<name>=<value>; capture the part before the '='.
	xFlag := regexp.MustCompile(`-X\s+['"]?([^\s'"=]+)=`)

	for _, name := range []string{"Dockerfile", ".goreleaser.yaml"} {
		b, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}

		matches := xFlag.FindAllStringSubmatch(string(b), -1)
		if len(matches) == 0 {
			t.Errorf("%s: no -X ldflag found; the version stamp was removed, so "+
				"released binaries will report the default", name)
			continue
		}
		for _, m := range matches {
			if got := m[1]; got != want {
				t.Errorf("%s: -X targets %q, but the module path in go.mod makes it %q.\n"+
					"The linker ignores an -X it cannot resolve, so this build would "+
					"succeed and ship a binary reporting the default version.", name, got, want)
			}
		}
	}
}

func modulePathFrom(t *testing.T, gomod string) string {
	t.Helper()
	b, err := os.ReadFile(gomod)
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	for line := range strings.SplitSeq(string(b), "\n") {
		if after, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.TrimSpace(after)
		}
	}
	t.Fatal("no module directive in go.mod")
	return ""
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod found above the test's working directory")
		}
		dir = parent
	}
}
