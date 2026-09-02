package evaluator_test

import (
	"testing"

	"github.com/gsoultan/Hermod/pkg/comm/message"
	"github.com/gsoultan/Hermod/pkg/infra/evaluator"
)

// "$.field" is JSONPath for "field at the document root" — a syntax marker, not
// a different lookup. The UI teaches it, every transformer accepts it as a
// keyField/targetField, and users mix the two forms freely.
//
// They did not resolve the same way. The "$." branch tried the raw payload
// first and the data map second, then gave up; the bare form tried the data map
// first and had five further fallbacks. So "$.x" could return nil where "x"
// returned a value, and a db_lookup whose keyField was nil returned the message
// silently unenriched — no error, no default, just a missing field.
//
// Observed end to end: an identical CDC → db_lookup → Postgres workflow
// enriched 2000/2000 rows with keyField "customer_code" and 500/2000 (then
// 0/2000) with "$.customer_code".
func TestJSONPathRootIsParityWithBareField(t *testing.T) {
	build := func() *message.DefaultMessage {
		m := message.AcquireMessage()
		m.SetPayload([]byte(`{"customer_code":"C0002","nested":{"a":1}}`))
		return m
	}

	for _, path := range []string{"customer_code", "nested.a"} {
		t.Run(path, func(t *testing.T) {
			m := build()
			defer message.ReleaseMessage(m)

			bare := evaluator.GetMsgValByPath(m, path)
			rooted := evaluator.GetMsgValByPath(m, "$."+path)

			if bare == nil {
				t.Fatalf("precondition: bare %q resolved to nil", path)
			}
			if rooted != bare {
				t.Errorf("$.%s = %v, but %s = %v — the root prefix must not change resolution", path, rooted, path, bare)
			}
		})
	}
}

// A transform writes to the data map. The next node reading the same field must
// see the new value, not the value frozen in the original payload.
func TestJSONPathRootSeesTransformedValue(t *testing.T) {
	m := message.AcquireMessage()
	defer message.ReleaseMessage(m)
	m.SetPayload([]byte(`{"status":"pending"}`))

	m.SetData("status", "approved")

	if got := evaluator.GetMsgValByPath(m, "status"); got != "approved" {
		t.Errorf(`bare "status" = %v, want "approved"`, got)
	}
	if got := evaluator.GetMsgValByPath(m, "$.status"); got != "approved" {
		t.Errorf(`"$.status" = %v, want "approved" (read the stale payload instead of the data map)`, got)
	}
}

// The bare form falls back to the before-image, which is the only place a
// deleted row's columns exist. The rooted form must too.
func TestJSONPathRootFallsBackToBeforeImage(t *testing.T) {
	m := message.AcquireMessage()
	defer message.ReleaseMessage(m)
	m.SetBefore([]byte(`{"customer_code":"C0009"}`))

	bare := evaluator.GetMsgValByPath(m, "customer_code")
	rooted := evaluator.GetMsgValByPath(m, "$.customer_code")

	if bare == nil {
		t.Skip("bare form does not reach the before-image here; nothing to compare")
	}
	if rooted != bare {
		t.Errorf("$.customer_code = %v, but customer_code = %v", rooted, bare)
	}
}
