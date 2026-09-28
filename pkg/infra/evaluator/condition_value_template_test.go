package evaluator

import (
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// A condition's value is a template field: clicking a field in its picker
// inserts {{.after.status}}, and comparing a row with its own before-image —
// "did the status change" — is written {{.before.status}}. The field beside it
// resolves through the message, envelope and all. The value resolved through
// the bare data map, which for a CDC message *is* the after-image and has no
// "after", "before", "operation" or "meta" in it: every such token became "",
// so `=` was false for every row whatever it held, and `!=` true.
func TestConditionValueTokensResolveLikeTheField(t *testing.T) {
	builders := map[string]func(t *testing.T) hermod.Message{
		// What a CDC source hands the engine.
		"live": func(t *testing.T) hermod.Message {
			m := message.AcquireMessage()
			t.Cleanup(m.Release)
			m.SetOperation(hermod.OpUpdate)
			m.SetTable("orders")
			m.SetAfter([]byte(`{"status":"active","amount":1500}`))
			m.SetBefore([]byte(`{"status":"pending","amount":1500}`))
			m.SetMetadata("source", "pg")
			return m
		},
		// What the editor's simulation builds from a sample.
		"simulated": func(t *testing.T) hermod.Message {
			m := message.AcquireMessage()
			t.Cleanup(m.Release)
			message.PopulateFromMap(m, map[string]any{
				"operation": "update", "table": "orders",
				"after":    map[string]any{"status": "active", "amount": 1500},
				"before":   map[string]any{"status": "pending", "amount": 1500},
				"metadata": map[string]any{"source": "pg"},
			})
			return m
		},
	}
	cases := []struct {
		field, op, value string
		want             bool
	}{
		{"after.status", "=", "{{.after.status}}", true},
		{"status", "=", "{{.after.status}}", true},
		{"after.status", "!=", "{{.before.status}}", true},
		{"after.status", "=", "{{.before.status}}", false},
		{"after.amount", "=", "{{.before.amount}}", true},
		{"table", "=", "{{.table}}", true},
		{"operation", "=", "{{.operation}}", true},
		{"meta.source", "=", "{{.meta.source}}", true},
		{"status", "=", "{{ after.status }}", true},
	}
	for name, build := range builders {
		for _, tc := range cases {
			t.Run(name+"/"+tc.field+tc.op+tc.value, func(t *testing.T) {
				msg := build(t)
				cond := []map[string]any{{"field": tc.field, "operator": tc.op, "value": tc.value}}
				if got := EvaluateConditions(msg, cond); got != tc.want {
					t.Errorf("%s %s %s = %v, want %v (the field resolves to %v)",
						tc.field, tc.op, tc.value, got, tc.want, EvaluateField(msg, tc.field))
				}
			})
		}
	}
}

// Comparing a field with its own token has to hold for every type a row can
// carry, which pins the value to the reader the field uses. The raw resolver
// SQL templates use (MessageResolver) keeps Go types on purpose, and through
// stringify a []byte comes out "[97 98 99]" and a time.Time in Go's own layout,
// where the field reads base64 and RFC 3339.
func TestConditionValueTokenRendersEveryTypeLikeTheField(t *testing.T) {
	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	values := map[string]any{
		"raw":    []byte("abc"),
		"at":     time.Date(2026, 9, 28, 7, 0, 0, 0, time.UTC),
		"big":    int64(1704207845),
		"price":  99.5,
		"name":   "Ann",
		"active": true,
		"doc":    map[string]any{"b": 2, "a": 1},
		"tags":   []any{"x", "y"},
	}
	for k, v := range values {
		msg.SetData(k, v)
	}
	for k := range values {
		cond := []map[string]any{{"field": k, "operator": "=", "value": "{{." + k + "}}"}}
		if !EvaluateConditions(msg, cond) {
			t.Errorf("%s = {{.%s}} is false; the field reads %v", k, k, EvaluateField(msg, k))
		}
	}
}

// A token is data, never the process environment: env. stays unresolved.
func TestConditionValueTokenCannotReadTheEnvironment(t *testing.T) {
	t.Setenv("HERMOD_CONDITION_PROBE", "s3cr3t")
	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetData("guess", "s3cr3t")

	for _, token := range []string{"{{env.HERMOD_CONDITION_PROBE}}", "{{.env.HERMOD_CONDITION_PROBE}}"} {
		cond := []map[string]any{{"field": "guess", "operator": "=", "value": token}}
		if EvaluateConditions(msg, cond) {
			t.Errorf("%s resolved to the environment variable's value", token)
		}
	}
}
