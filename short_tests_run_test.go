package hermod

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every test skipped by -short must be run somewhere else.
//
// CI runs `go test -race -short ./...` because four worker load tests stand up
// 120 concurrent workflows apiece, and under Go 1.27 the race detector's shadow
// memory for those reached 8.73 GB against a 7GB runner. A separate step runs
// them at full scale without the detector.
//
// That split only works while the second step's -run expression actually names
// them. It stopped doing so almost immediately: TestWorkerSurvivesControlPlane-
// Outage carried a testing.Short() guard, matched none of the alternatives, and
// therefore ran nowhere at all — skipped by -short in both jobs and never
// selected by the load step. Nothing failed, because a test that does not run
// cannot fail. That is exactly the trade the split was introduced to avoid, and
// it happened anyway within a day.
//
// So the pairing is checked here rather than remembered. Adding a
// testing.Short() guard without extending the -run expression now fails the
// build, in the plain unit job, needing nothing but the source tree.

const ciWorkflow = ".github/workflows/ci.yml"

// This file discusses testing.Short() in order to search for it, so it would
// otherwise report itself — the same self-reference buildtags_test.go handles
// the same way.
const shortGuardSelfName = "short_tests_run_test.go"

// shortRunExpr is the -run expression on the load step, extracted from the
// workflow rather than duplicated, so this cannot pass by testing a stale copy.
var shortRunExpr = regexp.MustCompile(`-run '([^']+)'`)

// guardedTest matches a test function whose body reaches testing.Short().
var funcDecl = regexp.MustCompile(`func (Test[A-Za-z0-9_]+)\(`)

func TestEveryShortGuardedTestIsRunSomewhere(t *testing.T) {
	wf, err := os.ReadFile(ciWorkflow)
	if err != nil {
		t.Fatalf("reading %s: %v", ciWorkflow, err)
	}
	m := shortRunExpr.FindSubmatch(wf)
	if m == nil {
		t.Fatalf("no -run expression found in %s; if the load step was removed, every "+
			"testing.Short() guarded test now runs nowhere", ciWorkflow)
	}
	selector, err := regexp.Compile(string(m[1]))
	if err != nil {
		t.Fatalf("the load step's -run expression does not compile: %v", err)
	}

	var uncovered []string
	err = filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor", "dist", "build", "graphify-out", ".agents":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), "_test.go") || d.Name() == shortGuardSelfName {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(src)
		if !strings.Contains(text, "testing.Short()") {
			return nil
		}
		// Attribute each Short() to the test function it sits inside.
		for _, fn := range funcDecl.FindAllStringSubmatchIndex(text, -1) {
			name := text[fn[2]:fn[3]]
			start := fn[0]
			end := len(text)
			if next := funcDecl.FindStringIndex(text[fn[1]:]); next != nil {
				end = fn[1] + next[0]
			}
			if !strings.Contains(text[start:end], "testing.Short()") {
				continue
			}
			if !selector.MatchString(name) {
				uncovered = append(uncovered, name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the tree: %v", err)
	}

	if len(uncovered) > 0 {
		t.Errorf("these tests are skipped by -short and matched by nothing in the load "+
			"step's -run expression, so they run nowhere in CI:\n\t%s\n\n"+
			"add them to the -run expression in %s, or drop the testing.Short() guard. "+
			"A test that does not run cannot fail, which is why nothing else catches this.",
			strings.Join(uncovered, "\n\t"), ciWorkflow)
	}
}
