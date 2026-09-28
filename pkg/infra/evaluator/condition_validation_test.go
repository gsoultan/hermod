package evaluator

// An invalid regex in a router condition rejected every message, silently.
//
// `match` starts false and the compile error was swallowed (`if err == nil`),
// so a condition whose pattern does not compile takes the false branch for
// every message, for ever, with nothing logged and no error anywhere. A typo
// in a filter is therefore indistinguishable from "no rows matched" — the
// workflow stays green while it drops 100% of its traffic.
//
// Same shape as the `7d` retention parse: a parse failure on operator-supplied
// config that silently disables something. Those must be loud.

import (
	"strings"
	"testing"
)

func TestValidateConditionsRejectsUncompilablePatterns(t *testing.T) {
	cases := []struct {
		name       string
		conditions []map[string]any
		wantErr    bool
		wantIn     string
	}{
		{
			name:       "no conditions",
			conditions: nil,
		},
		{
			name:       "valid regex",
			conditions: []map[string]any{{"field": "f", "operator": "regex", "value": `^a.c$`}},
		},
		{
			name:       "valid not_regex",
			conditions: []map[string]any{{"field": "f", "operator": "not_regex", "value": `^a.c$`}},
		},
		{
			name:       "non-regex operator with nonsense value",
			conditions: []map[string]any{{"field": "f", "operator": "eq", "value": `[unclosed`}},
		},
		{
			name:       "invalid regex",
			conditions: []map[string]any{{"field": "f", "operator": "regex", "value": `[unclosed`}},
			wantErr:    true,
			wantIn:     "[unclosed",
		},
		{
			name:       "invalid not_regex",
			conditions: []map[string]any{{"field": "status", "operator": "not_regex", "value": `a(b`}},
			wantErr:    true,
			wantIn:     "status",
		},
		{
			name: "one good one bad",
			conditions: []map[string]any{
				{"field": "a", "operator": "regex", "value": `^ok$`},
				{"field": "b", "operator": "regex", "value": `*nope`},
			},
			wantErr: true,
			wantIn:  "*nope",
		},
		{
			// A templated pattern is resolved per message against that message's
			// data, so it cannot be judged here. Validation must not reject a
			// workflow for it.
			name:       "templated pattern is not judged",
			conditions: []map[string]any{{"field": "f", "operator": "regex", "value": `{{.pattern}}`}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateConditions(tc.conditions)
			if tc.wantErr && err == nil {
				t.Fatalf("ValidateConditions(%v) = nil, want an error", tc.conditions)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("ValidateConditions(%v) = %v, want nil", tc.conditions, err)
			}
			if tc.wantErr && !strings.Contains(err.Error(), tc.wantIn) {
				t.Errorf("error %q does not name %q, so it cannot be acted on", err, tc.wantIn)
			}
		})
	}
}

// The editor offers ten operators and the engine accepts six more spellings of
// them. Anything else reaches no case in EvaluateConditions and fails every
// message, which is what the save-time check reports. That check and the
// evaluator have to agree on the list: every operator it accepts must be one
// the evaluator can match with, and every spelling it rejects must be one the
// evaluator never matches with.
func TestConditionOperatorsAreTheOnesEvaluated(t *testing.T) {
	matching := map[string]struct {
		field any
		value string
	}{
		"=": {"a", "a"}, "eq": {"a", "a"},
		"!=": {"a", "b"}, "neq": {"a", "b"},
		">": {2.0, "1"}, "gt": {2.0, "1"},
		">=": {1.0, "1"}, "gte": {1.0, "1"},
		"<": {1.0, "2"}, "lt": {1.0, "2"},
		"<=": {1.0, "1"}, "lte": {1.0, "1"},
		"contains": {"abc", "b"}, "not_contains": {"abc", "z"},
		"regex": {"abc", "^a"}, "not_regex": {"abc", "^z"},
	}
	for op := range conditionOperators {
		in, ok := matching[op]
		if !ok {
			t.Errorf("operator %q is accepted at save time but has no matching case here", op)
			continue
		}
		msg := &mockMessage{data: map[string]any{"f": in.field}}
		cond := []map[string]any{{"field": "f", "operator": op, "value": in.value}}
		if !EvaluateConditions(msg, cond) {
			t.Errorf("operator %q is accepted at save time but EvaluateConditions never applies it", op)
		}
	}
	if len(matching) != len(conditionOperators) {
		t.Errorf("%d operators have a matching case and %d are accepted", len(matching), len(conditionOperators))
	}

	for _, op := range []string{"", "==", "===", "equals", "EQ", "like", "in", "<>"} {
		if IsConditionOperator(op) {
			t.Errorf("IsConditionOperator(%q) = true", op)
		}
		msg := &mockMessage{data: map[string]any{"f": "a"}}
		cond := []map[string]any{{"field": "f", "operator": op, "value": "a"}}
		if EvaluateConditions(msg, cond) {
			t.Errorf("operator %q matched, so rejecting it at save time would refuse a working condition", op)
		}
	}
}
