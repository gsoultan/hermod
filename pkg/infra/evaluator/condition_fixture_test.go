package evaluator

import (
	"encoding/json"
	"os"
	"testing"
)

// The condition contract is one file, read here and by
// ui/src/__tests__/matchesConditionParity.test.ts.
//
// It used to be two tables, the TypeScript one "transcribed" from the Go one.
// That arrangement catches a TypeScript regression against a frozen snapshot of
// Go and nothing else: when the Go side changes, nothing makes the
// transcription fail, so the editor keeps asserting the old truth -- green --
// while the twins have drifted. The editor's preview then lies, which is worse
// than no preview, because an operator tunes a condition until the editor says
// it matches and ships something that does not.
//
// Reading one fixture makes adding a case a single edit and makes a one-sided
// change fail on the other side for real.

const conditionFixturePath = "testdata/condition_cases.json"

type operatorCase struct {
	Operator string `json:"operator"`
	Field    any    `json:"field"`
	Value    any    `json:"value"`
}

type numericCase struct {
	Name  string `json:"name"`
	Field any    `json:"field"`
	Value string `json:"value"`
	Equal bool   `json:"equal"`
}

type wideCase struct {
	Operator string `json:"operator"`
	Value    string `json:"value"`
	Expected bool   `json:"expected"`
}

type conditionFixture struct {
	Operators struct {
		Matching    []operatorCase `json:"matching"`
		NotMatching []operatorCase `json:"notMatching"`
	} `json:"operators"`
	NumericEquality struct {
		Cases []numericCase `json:"cases"`
	} `json:"numericEquality"`
	WideInteger struct {
		Field any        `json:"field"`
		Cases []wideCase `json:"cases"`
	} `json:"wideInteger"`
}

func loadConditionFixture(t *testing.T) conditionFixture {
	t.Helper()
	raw, err := os.ReadFile(conditionFixturePath)
	if err != nil {
		t.Fatalf("read %s: %v", conditionFixturePath, err)
	}
	var f conditionFixture
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("parse %s: %v", conditionFixturePath, err)
	}
	return f
}

func matches(field any, operator string, value any) bool {
	return EvaluateConditions(numberMsg("f", field),
		[]map[string]any{{"field": "f", "operator": operator, "value": value}})
}

// A fixture that has been emptied would let every test below pass by having
// nothing to run, which is the quietest way for a shared contract to stop being
// one. The counts are a floor, not an assertion about the exact contents.
func TestTheConditionFixtureIsNotEmpty(t *testing.T) {
	f := loadConditionFixture(t)

	if len(f.Operators.Matching) < 16 {
		t.Errorf("operators.matching has %d cases; every operator the editor offers needs one",
			len(f.Operators.Matching))
	}
	if len(f.Operators.NotMatching) == 0 {
		t.Error("operators.notMatching is empty; without it a blanket `return true` passes")
	}
	if len(f.NumericEquality.Cases) < 13 {
		t.Errorf("numericEquality has %d cases, want at least 13", len(f.NumericEquality.Cases))
	}
	if len(f.WideInteger.Cases) == 0 {
		t.Error("wideInteger is empty; this is the case that broke in production")
	}

	// Every operator offered in the editor must appear. A case removed here is
	// a case removed from the TypeScript side too, silently.
	seen := map[string]bool{}
	for _, c := range f.Operators.Matching {
		seen[c.Operator] = true
	}
	for _, op := range []string{
		"=", "!=", ">", ">=", "<", "<=",
		"contains", "not_contains", "regex", "not_regex",
		"eq", "neq", "gt", "gte", "lt", "lte",
	} {
		if !seen[op] {
			t.Errorf("operator %q is offered in the editor and has no case in the fixture", op)
		}
	}
}

func TestConditionOperatorsFromTheSharedFixture(t *testing.T) {
	f := loadConditionFixture(t)

	for _, c := range f.Operators.Matching {
		t.Run("matches/"+c.Operator, func(t *testing.T) {
			if !matches(c.Field, c.Operator, c.Value) {
				t.Errorf("%q did not match %#v against %#v; it is offered in the editor",
					c.Operator, c.Field, c.Value)
			}
		})
	}
	for _, c := range f.Operators.NotMatching {
		t.Run("rejects/"+c.Operator, func(t *testing.T) {
			if matches(c.Field, c.Operator, c.Value) {
				t.Errorf("%q matched %#v against %#v; it is a real negation, not a constant true",
					c.Operator, c.Field, c.Value)
			}
		})
	}
}

func TestNumericEqualityFromTheSharedFixture(t *testing.T) {
	for _, c := range loadConditionFixture(t).NumericEquality.Cases {
		t.Run(c.Name, func(t *testing.T) {
			eq := matches(c.Field, "=", c.Value)
			if eq != c.Equal {
				t.Errorf("%#v = %q gave %v, want %v", c.Field, c.Value, eq, c.Equal)
			}
			// != must be the exact negation, or a config can satisfy both.
			if ne := matches(c.Field, "!=", c.Value); ne == eq {
				t.Errorf("%#v: = and != both returned %v for %q", c.Field, eq, c.Value)
			}
		})
	}
}

func TestWideIntegerFromTheSharedFixture(t *testing.T) {
	f := loadConditionFixture(t)
	for _, c := range f.WideInteger.Cases {
		t.Run(c.Operator+" "+c.Value, func(t *testing.T) {
			if got := matches(f.WideInteger.Field, c.Operator, c.Value); got != c.Expected {
				t.Errorf("%v %s %q gave %v, want %v", f.WideInteger.Field, c.Operator, c.Value, got, c.Expected)
			}
		})
	}
}
