package security

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// uiOptionsPath is the editor's hand-written copy of the algorithm table.
const uiOptionsPath = "../../../../ui/src/components/workflow/Transformation/configs/security/cipherOptions.ts"

var (
	uiAlgorithmRE = regexp.MustCompile(`\{\s*value:\s*'([a-z0-9-]+)',\s*label:\s*'[^']*',\s*authenticated:\s*(true|false)\s*\}`)
	uiDefaultRE   = regexp.MustCompile(`DEFAULT_ALGORITHM\s*=\s*'([a-z0-9-]+)'`)
)

// TestUIAlgorithmListMatchesBackend keeps the picker honest.
//
// The editor cannot import the Go table, so it restates it. A restated list
// drifts: the picker offers an algorithm the backend has never heard of and the
// node fails at runtime with "unknown algorithm", or the backend gains one that
// no operator can select. This test is the thing that makes adding an algorithm
// in only one place a build failure instead of a support ticket.
func TestUIAlgorithmListMatchesBackend(t *testing.T) {
	src, err := os.ReadFile(filepath.Clean(uiOptionsPath))
	if err != nil {
		t.Fatalf("cannot read the editor's option list at %s: %v", uiOptionsPath, err)
	}

	matches := uiAlgorithmRE.FindAllStringSubmatch(string(src), -1)
	if len(matches) == 0 {
		t.Fatalf("no algorithm entries found in %s; has ALGORITHM_OPTIONS been reformatted?", uiOptionsPath)
	}

	uiAuthenticated := make(map[string]bool, len(matches))
	uiNames := make([]string, 0, len(matches))
	for _, m := range matches {
		if _, dup := uiAuthenticated[m[1]]; dup {
			t.Errorf("the editor lists %q twice", m[1])
		}
		uiAuthenticated[m[1]] = m[2] == "true"
		uiNames = append(uiNames, m[1])
	}
	sort.Strings(uiNames)

	backend := SupportedAlgorithms()
	if strings.Join(uiNames, ",") != strings.Join(backend, ",") {
		t.Errorf("algorithm lists have drifted:\n  editor : %s\n  backend: %s",
			strings.Join(uiNames, ", "), strings.Join(backend, ", "))
	}

	// The authenticated flag drives a security warning in the editor, so a wrong
	// value there tells an operator that an unauthenticated mode detects
	// tampering. That is worse than an absent warning.
	for _, name := range backend {
		if want, got := AlgorithmIsAuthenticated(name), uiAuthenticated[name]; want != got {
			t.Errorf("%s: editor says authenticated=%t, backend says %t", name, got, want)
		}
	}
}

// TestUIDefaultAlgorithmMatchesBackend: if the editor pre-selected a different
// default, a node created without touching the algorithm control would encrypt
// under one algorithm while an untouched node encrypted under another.
func TestUIDefaultAlgorithmMatchesBackend(t *testing.T) {
	src, err := os.ReadFile(filepath.Clean(uiOptionsPath))
	if err != nil {
		t.Fatalf("cannot read the editor's option list: %v", err)
	}

	m := uiDefaultRE.FindStringSubmatch(string(src))
	if m == nil {
		t.Fatal("DEFAULT_ALGORITHM not found in the editor's option list")
	}
	if m[1] != defaultAlgorithm {
		t.Errorf("default algorithm has drifted: editor %q, backend %q", m[1], defaultAlgorithm)
	}
}
