package evaluator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Every file in testdata/functions is one expression function's contract, read
// here and by ui/src/__tests__/functionParity.test.ts, for the reason
// condition_fixture_test.go gives: the editor evaluates expressions itself, and
// two tables kept by hand catch only one side drifting from the other. A case
// is a whole expression evaluated against the file's `source`, so the argument
// parser on each side runs too.
//
// One file per function family, so changes to different functions never edit
// the same file.

const functionFixtureGlob = "testdata/functions/*.json"

type functionCase struct {
	Name string `json:"name"`
	Expr string `json:"expr"`
	Want any    `json:"want"`
}

type functionFixture struct {
	Source map[string]any `json:"source"`
	Cases  []functionCase `json:"cases"`
}

func fixtureJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %#v: %v", v, err)
	}
	return string(b)
}

func TestFunctionsFromTheSharedFixtures(t *testing.T) {
	paths, err := filepath.Glob(functionFixtureGlob)
	if err != nil {
		t.Fatalf("glob %s: %v", functionFixtureGlob, err)
	}
	if len(paths) == 0 {
		t.Fatalf("no fixtures match %s", functionFixtureGlob)
	}

	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			var f functionFixture
			if err := json.Unmarshal(raw, &f); err != nil {
				t.Fatalf("parse: %v", err)
			}
			// An emptied file would pass by having nothing to run.
			if len(f.Cases) == 0 {
				t.Fatal("no cases")
			}

			msg := &mockMessage{data: f.Source}
			for _, c := range f.Cases {
				t.Run(c.Name, func(t *testing.T) {
					// Compared as JSON, so an empty list and null stay apart and
					// a number compares as a number whatever Go type holds it.
					got := NewEvaluator().ParseAndEvaluate(msg, c.Expr)
					if g, w := fixtureJSON(t, got), fixtureJSON(t, c.Want); g != w {
						t.Errorf("%s\n got %s\nwant %s", c.Expr, g, w)
					}
				})
			}
		})
	}
}
