package message

import (
	"encoding/json"
	"testing"

	"github.com/gsoultan/hermod"
)

// A source payload that is not a JSON object — a bare string, a scalar, an
// array, or text that is not JSON at all — must still reach the sink. Before
// this was fixed, MarshalJSON discarded the unmarshal error and emitted only
// the system fields, so the body vanished with no error anywhere.
func TestMarshalJSON_NonObjectPayload_PreservesBody(t *testing.T) {
	cases := []struct {
		name      string
		payload   string
		wantValue any
	}{
		{"plain text", `hello world`, "hello world"},
		{"csv line", `id,name,qty`, "id,name,qty"},
		{"quoted json string", `"hello world"`, "hello world"},
		{"number", `42`, float64(42)},
		{"bool", `true`, true},
		{"array", `[1,2,3]`, []any{float64(1), float64(2), float64(3)}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := AcquireMessage()
			defer ReleaseMessage(m)
			m.SetID("id-1")
			m.SetPayload([]byte(tc.payload))

			out, err := m.MarshalJSON()
			if err != nil {
				t.Fatalf("MarshalJSON returned error: %v", err)
			}

			var got map[string]any
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatalf("output is not valid JSON (%s): %v", out, err)
			}

			raw, ok := got[NonObjectPayloadKey]
			if !ok {
				t.Fatalf("body was dropped: %q produced %s", tc.payload, out)
			}
			if !jsonEqual(raw, tc.wantValue) {
				t.Errorf("payload = %#v, want %#v (output %s)", raw, tc.wantValue, out)
			}
			if got["id"] != "id-1" {
				t.Errorf("system field id lost: %s", out)
			}
		})
	}
}

// A JSON object payload keeps its long-standing behaviour: fields merge into
// the root and no "raw" key appears.
func TestMarshalJSON_ObjectPayload_Unchanged(t *testing.T) {
	m := AcquireMessage()
	defer ReleaseMessage(m)
	m.SetID("id-1")
	m.SetPayload([]byte(`{"a":1,"b":"x"}`))

	out, err := m.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON returned error: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if got["a"] != float64(1) || got["b"] != "x" {
		t.Errorf("object fields not merged into root: %s", out)
	}
	if _, ok := got[NonObjectPayloadKey]; ok {
		t.Errorf("object payload must not be wrapped: %s", out)
	}
}

// The same payload must serialise identically whether or not a transformation
// happened to call Data() first. Previously Data() lazily populated
// data["payload"] while MarshalJSON ran its own unmarshal that could not
// handle arrays, so output depended on pipeline shape.
func TestMarshalJSON_DeterministicRegardlessOfDataAccess(t *testing.T) {
	for _, payload := range []string{`[1,2,3]`, `hello world`, `42`, `{"a":1}`} {
		t.Run(payload, func(t *testing.T) {
			untouched := AcquireMessage()
			defer ReleaseMessage(untouched)
			untouched.SetID("id-1")
			untouched.SetPayload([]byte(payload))
			outA, err := untouched.MarshalJSON()
			if err != nil {
				t.Fatalf("MarshalJSON (untouched) error: %v", err)
			}

			touched := AcquireMessage()
			defer ReleaseMessage(touched)
			touched.SetID("id-1")
			touched.SetPayload([]byte(payload))
			_ = touched.Data() // a filter or mapping reads the message first
			outB, err := touched.MarshalJSON()
			if err != nil {
				t.Fatalf("MarshalJSON (after Data()) error: %v", err)
			}

			if string(outA) != string(outB) {
				t.Errorf("output depends on Data() access:\n  without: %s\n  with:    %s", outA, outB)
			}
		})
	}
}

// Transformations address fields through Data(); a non-object payload has to be
// reachable there too, otherwise a JSONPath mapping has nothing to select.
func TestData_NonObjectPayload_ExposesPayloadKey(t *testing.T) {
	m := AcquireMessage()
	defer ReleaseMessage(m)
	m.SetPayload([]byte(`hello world`))

	if got := m.Data()[NonObjectPayloadKey]; got != "hello world" {
		t.Errorf(`Data()[%q] = %#v, want "hello world"`, NonObjectPayloadKey, got)
	}
}

// A CDC message whose payload is not JSON used to fail marshalling outright:
// the payload went into a json.RawMessage, which rejects non-JSON bytes.
func TestMarshalJSON_CDCNonJSONPayload_DoesNotError(t *testing.T) {
	m := AcquireMessage()
	defer ReleaseMessage(m)
	m.SetID("id-1")
	m.SetOperation(hermod.Operation("insert"))
	m.SetTable("t")
	m.SetPayload([]byte(`hello world`))

	out, err := m.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON returned error for non-JSON CDC payload: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	after, ok := got["after"].(map[string]any)
	if !ok {
		t.Fatalf("after missing or not an object: %s", out)
	}
	if after[NonObjectPayloadKey] != "hello world" {
		t.Errorf(`after[%q] = %#v, want "hello world" (output %s)`, NonObjectPayloadKey, after[NonObjectPayloadKey], out)
	}
}

// CDC with a JSON object payload keeps emitting the row image verbatim.
func TestMarshalJSON_CDCObjectPayload_Unchanged(t *testing.T) {
	m := AcquireMessage()
	defer ReleaseMessage(m)
	m.SetID("id-1")
	m.SetOperation(hermod.Operation("insert"))
	m.SetPayload([]byte(`{"a":1}`))

	out, err := m.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON error: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	after, ok := got["after"].(map[string]any)
	if !ok || after["a"] != float64(1) {
		t.Errorf("CDC after payload changed: %s", out)
	}
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// Edge cases a non-object payload must not disturb.
func TestMarshalJSON_NonObjectPayload_EdgeCases(t *testing.T) {
	t.Run("object owning a payload field is untouched", func(t *testing.T) {
		m := AcquireMessage()
		defer ReleaseMessage(m)
		m.SetPayload([]byte(`{"payload":5,"a":1}`))

		var got map[string]any
		out, err := m.MarshalJSON()
		if err != nil {
			t.Fatalf("MarshalJSON: %v", err)
		}
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		if got["payload"] != float64(5) || got["a"] != float64(1) {
			t.Errorf("object fields clobbered: %s", out)
		}
	})

	t.Run("json null adds no key", func(t *testing.T) {
		m := AcquireMessage()
		defer ReleaseMessage(m)
		m.SetID("id-1")
		m.SetPayload([]byte(`null`))

		out, err := m.MarshalJSON()
		if err != nil {
			t.Fatalf("MarshalJSON: %v", err)
		}
		var got map[string]any
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		if _, ok := got[NonObjectPayloadKey]; ok {
			t.Errorf("null payload should not synthesise a key: %s", out)
		}
	})

	t.Run("ToMap agrees with MarshalJSON", func(t *testing.T) {
		for _, payload := range []string{`hello world`, `[1,2,3]`, `42`, `{"a":1}`} {
			m := AcquireMessage()
			m.SetID("id-1")
			m.SetPayload([]byte(payload))

			out, err := m.MarshalJSON()
			if err != nil {
				t.Fatalf("MarshalJSON(%s): %v", payload, err)
			}
			var fromJSON map[string]any
			if err := json.Unmarshal(out, &fromJSON); err != nil {
				t.Fatalf("invalid JSON: %v", err)
			}

			viaMap, err := json.Marshal(m.ToMap())
			if err != nil {
				t.Fatalf("marshal ToMap(%s): %v", payload, err)
			}
			var fromMap map[string]any
			if err := json.Unmarshal(viaMap, &fromMap); err != nil {
				t.Fatalf("invalid JSON: %v", err)
			}

			if fromJSON[NonObjectPayloadKey] == nil && payload != `{"a":1}` {
				t.Errorf("MarshalJSON dropped body for %s: %s", payload, out)
			}
			if !jsonEqual(fromJSON[NonObjectPayloadKey], fromMap[NonObjectPayloadKey]) {
				t.Errorf("ToMap and MarshalJSON disagree for %s:\n  json: %s\n  map:  %s", payload, out, viaMap)
			}
			ReleaseMessage(m)
		}
	})
}
