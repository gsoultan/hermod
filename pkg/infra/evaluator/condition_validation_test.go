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
