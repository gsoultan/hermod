package evaluator

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/hermod/pkg/comm/message"
)

func TestGetMsgValByPath(t *testing.T) {
	msg := &mockMessage{
		id: "msg-1",
		data: map[string]any{
			"after": map[string]any{
				"id":   123,
				"name": "After Name",
			},
		},
		metadata: map[string]string{
			"source_id": "s1",
		},
		op:    "create",
		table: "users",
	}

	tests := []struct {
		path     string
		expected any
	}{
		{"after.id", 123},
		{"id", "msg-1"}, // should resolve from msg.ID()
		{"after.name", "After Name"},
		{"name", "After Name"},
		{"operation", "create"},
		{"table", "users"},
		{"metadata.source_id", "s1"},
		{"meta.source_id", "s1"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := GetMsgValByPath(msg, tt.path)
			// Simple comparison for test
			if fmt.Sprintf("%v", got) != fmt.Sprintf("%v", tt.expected) {
				t.Errorf("GetMsgValByPath(%s) = %v, want %v", tt.path, got, tt.expected)
			}
		})
	}
}

func TestEvaluateConditions_Regex(t *testing.T) {
	msg := &mockMessage{data: map[string]any{
		"status": "error_404",
		"email":  "test@example.com",
	}}

	tests := []struct {
		name       string
		conditions []map[string]any
		expected   bool
	}{
		{
			"Regex match",
			[]map[string]any{
				{"field": "status", "operator": "regex", "value": "error_.*"},
			},
			true,
		},
		{
			"Regex no match",
			[]map[string]any{
				{"field": "status", "operator": "regex", "value": "success_.*"},
			},
			false,
		},
		{
			"Not Regex match",
			[]map[string]any{
				{"field": "status", "operator": "not_regex", "value": "success_.*"},
			},
			true,
		},
		{
			"Email regex",
			[]map[string]any{
				{"field": "email", "operator": "regex", "value": `^[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,4}$`},
			},
			true,
		},
		{
			"Not contains",
			[]map[string]any{
				{"field": "status", "operator": "not_contains", "value": "ok"},
			},
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EvaluateConditions(msg, tt.conditions); got != tt.expected {
				t.Errorf("EvaluateConditions() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestEvaluateConditions_NumericTrimAndMissing(t *testing.T) {
	msg := &mockMessage{data: map[string]any{
		"num":    1,
		"status": "ok",
	}}

	// Numeric comparator should trim surrounding spaces in value
	conds1 := []map[string]any{
		{"field": "num", "operator": ">=", "value": " 1 "},
	}
	if !EvaluateConditions(msg, conds1) {
		t.Errorf("expected numeric comparator with whitespace to pass")
	}

	// Missing field should stringify to empty string and match empty value
	conds2 := []map[string]any{
		{"field": "missing_field", "operator": "=", "value": ""},
	}
	if !EvaluateConditions(msg, conds2) {
		t.Errorf("expected missing field to equal empty string")
	}
}

func TestEvaluateConditions_ValueTemplateResolution(t *testing.T) {
	msg := &mockMessage{data: map[string]any{
		"status":   "ready",
		"expected": "ready",
	}}

	conds := []map[string]any{
		{"field": "status", "operator": "=", "value": "{{.expected}}"},
	}

	if !EvaluateConditions(msg, conds) {
		t.Errorf("expected template value to resolve and match")
	}
}

func TestEvaluateConditions_CDCEnvelopeAliasing(t *testing.T) {
	// Case 1: Root-only payload, condition uses after.id
	msgRoot := &mockMessage{data: map[string]any{
		"id":   1,
		"name": "alice",
	}}

	condsAfter := []map[string]any{
		{"field": "after.id", "operator": "=", "value": "1"},
	}
	if !EvaluateConditions(msgRoot, condsAfter) {
		t.Errorf("expected after.id to resolve to root id when after is absent")
	}

	// Case 2: After-only payload, condition uses root id
	msgAfter := &mockMessage{data: map[string]any{
		"after": map[string]any{
			"id":   2,
			"name": "bob",
		},
	}}

	condsRoot := []map[string]any{
		{"field": "id", "operator": "=", "value": "2"},
	}
	if !EvaluateConditions(msgAfter, condsRoot) {
		t.Errorf("expected root id to resolve to after.id when only after exists")
	}
}

func TestGetMsgValByPath_AfterFallback(t *testing.T) {
	msg := &mockMessage{
		data: map[string]any{
			"id":   1,
			"name": "alice",
		},
		op: "update",
	}

	// For CDC events, if we ask for "after.field", it should find it in the flat data map
	// even if there is no explicit "after" nesting in the data map.
	tests := []struct {
		path     string
		expected any
	}{
		{"after.id", 1},
		{"after.name", "alice"},
		{"name", "alice"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := GetMsgValByPath(msg, tt.path)
			if fmt.Sprintf("%v", got) != fmt.Sprintf("%v", tt.expected) {
				t.Errorf("GetMsgValByPath(%s) = %v, want %v", tt.path, got, tt.expected)
			}
		})
	}
}

func TestEvaluateConditions_CDCMetaFields(t *testing.T) {
	msg := &mockMessage{
		op:     "update",
		table:  "users",
		schema: "public",
		data:   map[string]any{"after": map[string]any{"id": 10}},
	}

	tests := []struct {
		name     string
		conds    []map[string]any
		expected bool
	}{
		{"Operation by name", []map[string]any{{"field": "operation", "operator": "=", "value": "update"}}, true},
		{"Operation alias op", []map[string]any{{"field": "op", "operator": "!=", "value": "delete"}}, true},
		{"Table filter", []map[string]any{{"field": "table", "operator": "=", "value": "users"}}, true},
		{"Schema filter", []map[string]any{{"field": "schema", "operator": "=", "value": "public"}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EvaluateConditions(msg, tt.conds); got != tt.expected {
				t.Errorf("EvaluateConditions() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestEvaluateConditions_DateAndFunctions(t *testing.T) {
	m := &mockMessage{
		data: map[string]any{
			"created_at": "2023-06-23T10:00:00Z",
			"status":     "ACTIVE",
			"amount":     "100.50",
		},
	}

	tests := []struct {
		name       string
		conditions []map[string]any
		expected   bool
	}{
		{
			"Date comparison",
			[]map[string]any{
				{"field": "todate(source.created_at)", "operator": ">", "value": "2023-01-01"},
			},
			true,
		},
		{
			"Date comparison false",
			[]map[string]any{
				{"field": "todate(source.created_at)", "operator": "<", "value": "2023-01-01"},
			},
			false,
		},
		{
			"Function lower and regex",
			[]map[string]any{
				{"field": "lower(source.status)", "operator": "regex", "value": "active"},
			},
			true,
		},
		{
			"Function toint and numeric comparison",
			[]map[string]any{
				{"field": "toint(source.amount)", "operator": ">=", "value": 100},
			},
			true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := EvaluateConditions(m, tc.conditions); got != tc.expected {
				t.Errorf("%s: EvaluateConditions() = %v; want %v", tc.name, got, tc.expected)
			}
		})
	}
}

func TestResolveTemplate_Basic(t *testing.T) {
	data := map[string]any{
		"after": map[string]any{"id": 42, "name": "Bob"},
		"name":  "Alice",
	}
	tests := []struct {
		name string
		tpl  string
		want string
	}{
		{"plain", "hello world", "hello world"},
		{"leading dot", "id={{.name}}", "id=Alice"},
		{"nested path", "u={{after.name}}", "u=Bob"},
		{"numeric", "n={{after.id}}", "n=42"},
		{"missing field", "x={{after.missing}}!", "x=!"},
		{"multiple tokens", "{{.name}}/{{after.name}}", "Alice/Bob"},
		{"unterminated", "a {{ not closed", "a {{ not closed"},
		{"empty token", "v={{}}", "v="},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveTemplate(tc.tpl, data); got != tc.want {
				t.Errorf("ResolveTemplate(%q) = %q; want %q", tc.tpl, got, tc.want)
			}
		})
	}
}

// TestResolveTemplate_SelfReferentialTerminates is the regression test for the
// production 520: a data value that itself contains a {{...}} token referencing
// itself previously caused ResolveTemplate to loop forever (request hang).
// The substituted value must NOT be re-scanned, so resolution always terminates.
func TestResolveTemplate_SelfReferentialTerminates(t *testing.T) {
	data := map[string]any{
		"a": "{{a}}",            // self-referential
		"b": "{{.a}} and {{a}}", // expands to tokens that reference a
	}

	cases := []struct {
		name string
		tpl  string
		want string
	}{
		{"direct self reference", "{{.a}}", "{{a}}"},
		{"indirect", "{{.b}}", "{{.a}} and {{a}}"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			done := make(chan string, 1)
			go func() { done <- ResolveTemplate(tc.tpl, data) }()
			select {
			case got := <-done:
				if got != tc.want {
					t.Errorf("ResolveTemplate(%q) = %q; want %q", tc.tpl, got, tc.want)
				}
			case <-time.After(2 * time.Second):
				t.Fatalf("ResolveTemplate(%q) did not terminate (infinite loop)", tc.tpl)
			}
		})
	}
}

// TestEvaluateConditions_NilMessage ensures a nil message combined with a
// templated condition value does not panic (msg.Data() guard).
func TestEvaluateConditions_NilMessage(t *testing.T) {
	conds := []map[string]any{
		{"field": "name", "operator": "=", "value": "{{.name}}"},
	}
	// Must not panic; with no message data the template resolves to "" and the
	// missing field also resolves to "", so the equality holds.
	if !EvaluateConditions(nil, conds) {
		t.Errorf("expected nil-message condition to evaluate true without panicking")
	}
}

// ---------------------------------------------------------------------------
// A JSON request body is JSON, not text with holes in it.
//
// ResolveTemplateMsg writes each token's value as raw text. In a JSON body that
// broke in two ways an api_lookup operator hit: a jsonb field in quotes,
// "{{.after.profile}}", went out as its JSON text pasted inside a string --
// `"profile": "{"name":"Ada"}"`, which is not JSON at all -- and any value
// holding a quote did the same. ResolveJSONTemplateMsg resolves inside the JSON
// instead: a string that is one whole token and resolves to an object or array
// becomes that object or array, every other resolved string is escaped, and
// everything the operator wrote around the tokens is kept byte for byte.
// ---------------------------------------------------------------------------

func jsonTemplateMessage() *message.DefaultMessage {
	msg := message.AcquireMessage()
	message.PopulateFromMap(msg, map[string]any{
		"operation": "snapshot",
		"table":     "memberships",
		"after": map[string]any{
			"user_id":     "019a43e3-0000-7000-8000-000000000001",
			"entity_type": `region "north"`,
			"count":       42,
			"profile":     map[string]any{"name": "Ada", "roles": []any{"owner", "admin"}},
			"looks_like":  "{{.after.user_id}}",
		},
	})
	return msg
}

func TestResolveJSONTemplateMsg(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "a jsonb field in quotes is sent as the object it is",
			body: `{"profile": "{{.after.profile}}"}`,
			want: `{"profile": {"name":"Ada","roles":["owner","admin"]}}`,
		},
		{
			name: "a value holding a quote is escaped",
			body: `{"entity": "{{.after.entity_type}}"}`,
			want: `{"entity": "region \"north\""}`,
		},
		{
			name: "a scalar in quotes stays the string it always was",
			body: `{"count": "{{.after.count}}"}`,
			want: `{"count": "42"}`,
		},
		{
			name: "a token inside other text is text, escaped",
			body: `{"label": "type={{.after.entity_type}}!"}`,
			want: `{"label": "type=region \"north\"!"}`,
		},
		{
			name: "number literals are not re-encoded",
			body: `{"duration": 604800000000000, "big": 9007199254740993, "f": 1.50}`,
			want: `{"duration": 604800000000000, "big": 9007199254740993, "f": 1.50}`,
		},
		{
			name: "layout and key order are the operator's",
			body: "{\n  \"z\": \"{{.after.user_id}}\",\n  \"a\": [ \"{{.after.count}}\" ]\n}",
			want: "{\n  \"z\": \"019a43e3-0000-7000-8000-000000000001\",\n  \"a\": [ \"42\" ]\n}",
		},
		{
			name: "a missing field is the empty string, as before",
			body: `{"nope": "{{.after.nope}}"}`,
			want: `{"nope": ""}`,
		},
		{
			name: "the environment stays out of reach",
			body: `{"home": "{{env.HOME}}"}`,
			want: `{"home": ""}`,
		},
		{
			name: "a templated key resolves as a key",
			body: `{"{{.after.user_id}}": true}`,
			want: `{"019a43e3-0000-7000-8000-000000000001": true}`,
		},
		{
			name: "a value that looks like a token is not resolved again",
			body: `{"x": "{{.after.looks_like}}"}`,
			want: `{"x": "{{.after.user_id}}"}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := jsonTemplateMessage()
			defer msg.Release()

			got, ok := ResolveJSONTemplateMsg(tc.body, msg)
			if !ok {
				t.Fatalf("a valid JSON body was not resolved as JSON: %s", tc.body)
			}
			if got != tc.want {
				t.Errorf("resolved body\n got: %s\nwant: %s", got, tc.want)
			}
			if !json.Valid([]byte(got)) {
				t.Errorf("resolved body is not valid JSON: %s", got)
			}
		})
	}
}

// A body that is not JSON before resolution is left to ResolveTemplateMsg, so
// an unquoted token -- valid JSON only once it is filled in -- keeps working.
func TestResolveJSONTemplateMsgLeavesNonJSONBodies(t *testing.T) {
	msg := jsonTemplateMessage()
	defer msg.Release()

	for _, body := range []string{
		`{"profile": {{.after.profile}}}`,
		`user={{.after.user_id}}&type=x`,
		`{"a": 1} trailing`,
	} {
		if got, ok := ResolveJSONTemplateMsg(body, msg); ok {
			t.Errorf("%q was treated as JSON and resolved to %q", body, got)
		}
	}
	if got := ResolveTemplateMsg(`{"profile": {{.after.profile}}}`, msg); !strings.Contains(got, `"name":"Ada"`) {
		t.Errorf("the raw form stopped rendering an unquoted object: %s", got)
	}
}
