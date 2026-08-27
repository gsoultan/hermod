package hermod

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Tests that demand infrastructure must be behind the integration build tag.
//
// Integration tests in this repository fail loudly rather than skipping when
// they find themselves in CI without the infrastructure they need — the guard
// reads roughly:
//
//	if os.Getenv("HERMOD_INTEGRATION") != "1" || dsn == "" {
//		if os.Getenv("GITHUB_ACTIONS") == "true" {
//			t.Fatalf("... in CI, where PostgreSQL is started for exactly this")
//		}
//		t.Skip("integration: set HERMOD_INTEGRATION=1 and POSTGRES_DSN to run")
//	}
//
// That exists so a service that quietly stops being started turns the build red
// instead of turning a suite green by skipping all of it. It is only correct in
// the job that starts the services. The unit job deliberately starts nothing, so
// a file carrying that guard *without* the integration tag compiles into the
// unit job and fails there by construction — every run, for a reason that has
// nothing to do with the code under test.
//
// pkg/comm/sink/postgres/schema_evolution_test.go shipped exactly that way. It
// is worth being precise about why no local gate caught it: the fatal branch is
// reachable only when GITHUB_ACTIONS is set, so `go test ./...` on a workstation
// takes the t.Skip path and passes. The failure existed only in the environment
// that had not run yet.
//
// Hence this check, which runs in the plain unit job — the same place the bug
// lands — and needs no infrastructure of its own.
//
// Note the invariant is narrow on purpose. Plenty of test files read
// HERMOD_INTEGRATION and skip gracefully when it is unset; those are fine
// untagged and are none of this test's business. What must be tagged is the
// stricter thing: a file that turns a missing service into a failure.
const ciFatalMarker = "GITHUB_ACTIONS"

// This file names the marker in order to search for it, so it would otherwise
// report itself.
const selfName = "buildtags_test.go"

func TestATestThatFailsWithoutInfrastructureIsBehindTheIntegrationTag(t *testing.T) {
	var untagged []string

	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor", "dist", "build", "graphify-out":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), "_test.go") || d.Name() == selfName {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !strings.Contains(string(src), ciFatalMarker) {
			return nil
		}
		if !hasIntegrationTag(string(src)) {
			untagged = append(untagged, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the repository: %v", err)
	}

	if len(untagged) > 0 {
		t.Errorf("these test files fail when CI has no infrastructure, but are not "+
			"behind the integration build tag, so they compile into the unit job "+
			"that deliberately starts nothing and fail there on every run:\n\t%s\n\n"+
			"add `//go:build integration` above the package clause. Running "+
			"`go test ./...` locally will not reproduce it — the failing branch "+
			"needs GITHUB_ACTIONS=true.",
			strings.Join(untagged, "\n\t"))
	}
}

// hasIntegrationTag reports whether a build constraint selecting the integration
// tag appears before the package clause, which is the only place Go honours one.
func hasIntegrationTag(src string) bool {
	head := src
	if strings.HasPrefix(src, "package ") {
		head = ""
	} else if before, _, ok := strings.Cut(src, "\npackage "); ok {
		head = before
	}
	for line := range strings.SplitSeq(head, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "//go:build") && strings.Contains(line, "integration") {
			return true
		}
	}
	return false
}
