package message

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gsoultan/hermod"
)

// newCDCMsg builds a message the way pkg/comm/source/postgres builds one for an
// INSERT: SetOperation then SetAfter (postgres.go:1196,1213). The data map is
// empty and the fields materialise on first read.
func newCDCMsg() *DefaultMessage {
	m := AcquireMessage()
	m.SetOperation(hermod.OpCreate)
	m.SetTable("orders")
	m.SetAfter([]byte(`{"id":"1","amount":"10.50","customer_id":"7"}`))
	return m
}

// TestCDCMessageShapeDoesNotDependOnAccessOrder pins the invariant that broke
// three ways at once: what a message serialises to must not depend on whether
// something read it before the first write.
//
// SetData used to hydrate the payload differently from Data()/DataRef() -- on a
// CDC message it nested the row under "after" and left the written field at the
// root. A message written to without being read first then serialised four
// different ways: Payload() and ToMap() dropped the new field entirely, and
// MarshalJSON double-nested it as after.after. Only Data() had it.
//
// Every serialisation below must contain the written field, and must agree, for
// every access order.
func TestCDCMessageShapeDoesNotDependOnAccessOrder(t *testing.T) {
	orders := []struct {
		name string
		prep func(m *DefaultMessage)
	}{
		{"write without reading first", func(m *DefaultMessage) {
			m.SetData("customer_name", "ACME")
		}},
		{"read, then write", func(m *DefaultMessage) {
			_ = m.DataRef()
			m.SetData("customer_name", "ACME")
		}},
		{"ToMap first (tracing enabled), then write", func(m *DefaultMessage) {
			_ = m.ToMap()
			m.SetData("customer_name", "ACME")
		}},
		{"two writes, never read", func(m *DefaultMessage) {
			m.SetData("customer_name", "ACME")
			m.SetData("tier", "gold")
		}},
	}

	for _, tc := range orders {
		t.Run(tc.name, func(t *testing.T) {
			// Each serialisation on its own message: Payload() caches the bytes
			// it marshals, so calling it changes what a later MarshalJSON sees.
			mk := func() *DefaultMessage { m := newCDCMsg(); tc.prep(m); return m }

			data := mk().Data()
			payload := mk().Payload()
			toMap := mk().ToMap()
			marshalled, err := json.Marshal(mk())
			if err != nil {
				t.Fatalf("MarshalJSON: %v", err)
			}

			if data["customer_name"] != "ACME" {
				t.Errorf("Data() lost the written field: %v", data)
			}

			// The row and the written field both belong in the after-image.
			for _, want := range []string{"customer_name", "ACME", "customer_id"} {
				if !jsonHas(t, payload, want) {
					t.Errorf("Payload() is missing %q: %s", want, payload)
				}
			}

			afterFromToMap := afterOf(t, mustJSON(t, toMap))
			afterFromMarshal := afterOf(t, marshalled)

			if afterFromToMap != afterFromMarshal {
				t.Errorf("ToMap and MarshalJSON disagree on the after-image:\n  ToMap()       = %s\n  MarshalJSON() = %s",
					afterFromToMap, afterFromMarshal)
			}
			if !jsonHas(t, []byte(afterFromToMap), "customer_name") {
				t.Errorf("the after-image lost the written field: %s", afterFromToMap)
			}
			// A doubly-nested after is the specific corruption this guards.
			if jsonHas(t, []byte(afterFromToMap), `"after"`) {
				t.Errorf("the after-image is nested inside another after-image: %s", afterFromToMap)
			}
		})
	}
}

// TestSetDataKeepsANonObjectPayload guards the other half of the same defect:
// SetData's own unmarshal simply failed on a payload that is not a JSON object,
// stored nothing, and then cleared the payload bytes at the end of the call --
// so the first SetData destroyed a plain-text body outright.
func TestSetDataKeepsANonObjectPayload(t *testing.T) {
	m := AcquireMessage()
	m.SetPayload([]byte("a plain text body, not JSON"))
	m.SetData("seen_at", "2026-09-15")

	payload := m.Payload()
	if !jsonHas(t, payload, "a plain text body") {
		t.Errorf("the original body was dropped by SetData: %s", payload)
	}
	if !jsonHas(t, payload, "seen_at") {
		t.Errorf("the written field is missing: %s", payload)
	}
}

func jsonHas(t *testing.T, b []byte, want string) bool {
	t.Helper()
	return strings.Contains(string(b), want)
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// afterOf returns the "after" member of a serialised CDC envelope, as text.
func afterOf(t *testing.T, envelope []byte) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(envelope, &m); err != nil {
		t.Fatalf("unmarshal envelope %s: %v", envelope, err)
	}
	return string(m["after"])
}
