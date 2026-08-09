package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/user/hermod/pkg/comm/transformer"
)

// ---------------------------------------------------------------------------
// Transformer availability.
//
// Transformers register themselves from init(), which only runs if something
// links their package. Nothing does except a block of blank imports at the top
// of main.go, and nothing checks that block against reality. Add a transformer
// package and forget the import and it is silently absent from the binary: the
// registry lookup misses, and until recently applyTransformation responded to a
// miss by forwarding the message untransformed and reporting success.
//
// That is the same shape as the build-tag problem that left three integration
// test files uncompiled for months — a wiring step nothing verified. This test
// derives the expected set from the source rather than restating it, so a new
// transformer is covered the moment it is written rather than when someone
// remembers to update a list.
// ---------------------------------------------------------------------------

var registerCall = regexp.MustCompile(`Register\("([a-zA-Z_0-9]+)"`)

// registeredInSource returns every name the transformer packages claim to
// register, with the file that claims it.
func registeredInSource(t *testing.T) map[string]string {
	t.Helper()

	root := filepath.Join("..", "..", "pkg", "comm", "transformer")
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("cannot find the transformer packages at %s: %v", root, err)
	}

	found := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range registerCall.FindAllSubmatch(src, -1) {
			name := string(m[1])
			// The registry's own Register function and its tests are not
			// transformers; only calls that name a transformer count.
			if name == "" {
				continue
			}
			found[name] = path
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scanning transformer packages: %v", err)
	}
	if len(found) == 0 {
		t.Fatal("found no Register calls at all; this test is not checking anything")
	}
	return found
}

// TestEveryTransformerIsLinkedIntoTheBinary is the guard.
//
// This test lives in package main precisely so the binary's blank imports are
// linked into the test binary. A name that appears in the source but not in the
// registry means its package is not imported anywhere that matters.
func TestEveryTransformerIsLinkedIntoTheBinary(t *testing.T) {
	declared := registeredInSource(t)

	var missing []string
	for name, file := range declared {
		if _, ok := transformer.Get(name); !ok {
			missing = append(missing, name+" (declared in "+file+")")
		}
	}
	sort.Strings(missing)

	if len(missing) > 0 {
		t.Errorf("%d transformer(s) are registered in source but absent from the binary:\n  %s\n\n"+
			"Their package is not blank-imported by cmd/hermod/main.go, so its init() never runs. "+
			"A workflow using one would fail at runtime on a transformation the UI happily offers.",
			len(missing), strings.Join(missing, "\n  "))
	}
}

// TestTransformerScanFindsTheKnownPackages keeps the scan itself honest. If a
// refactor moved the packages or changed the Register signature, the walk above
// would quietly find nothing and pass.
func TestTransformerScanFindsTheKnownPackages(t *testing.T) {
	declared := registeredInSource(t)

	// A spread across the packages main imports: core, security, lookup,
	// advanced and logic are each represented.
	for _, name := range []string{"set", "mask", "db_lookup", "aggregate", "validate"} {
		if _, ok := declared[name]; !ok {
			names := make([]string, 0, len(declared))
			for n := range declared {
				names = append(names, n)
			}
			sort.Strings(names)
			t.Errorf("the source scan did not find %q; it found %v. The scan is looking in "+
				"the wrong place or the Register call shape changed, so the guard above "+
				"would pass while checking nothing.", name, names)
		}
	}
}
