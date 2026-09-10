package security

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/pkg/infra/evaluator"
)

// A column holding a whole JSON document is a common thing to encrypt: one
// sealed blob instead of a named field per value. Decrypting it returns a
// *string* that happens to contain JSON, which is not the same as an object —
// downstream nodes cannot address into it, and the live preview renders it as
// one escaped line rather than a tree. These tests cover turning it back into a
// value the rest of the pipeline can use.

const jsonDoc = `{"name":"Ada","contact":{"email":"ada@example.com"},"tags":["a","b"]}`

// sealJSON encrypts jsonDoc as a plain string field, which is what an upstream
// node or an external system would have written.
func sealJSON(t *testing.T, extra map[string]any) string {
	t.Helper()

	cfg := map[string]any{"field": "payload", "key": "a passphrase"}
	for k, v := range extra {
		cfg[k] = v
	}
	res, err := (&EncryptTransformer{}).Transform(t.Context(),
		newMsg(t, map[string]any{"payload": jsonDoc}), cfg)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	s, _ := res.Data()["payload"].(string)
	return s
}

// TestParseJSONOffKeepsTheString is the default. Silently changing a field's
// type would break every existing decrypt node, so parsing is opt-in.
func TestParseJSONOffKeepsTheString(t *testing.T) {
	sealed := sealJSON(t, nil)

	res, err := (&DecryptTransformer{}).Transform(t.Context(),
		newMsg(t, map[string]any{"payload": sealed}),
		map[string]any{"field": "payload", "key": "a passphrase"})
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if got, ok := res.Data()["payload"].(string); !ok || got != jsonDoc {
		t.Fatalf("default must return the plain string, got %#v", res.Data()["payload"])
	}
}

// TestParseJSONObjects turns the decrypted document into a real object.
func TestParseJSONObjects(t *testing.T) {
	sealed := sealJSON(t, nil)

	res, err := (&DecryptTransformer{}).Transform(t.Context(),
		newMsg(t, map[string]any{"payload": sealed}),
		map[string]any{"field": "payload", "key": "a passphrase", "parseJson": "objects"})
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}

	obj, ok := res.Data()["payload"].(map[string]any)
	if !ok {
		t.Fatalf("expected a map, got %T: %#v", res.Data()["payload"], res.Data()["payload"])
	}
	if obj["name"] != "Ada" {
		t.Errorf("name: got %v", obj["name"])
	}

	// The point of parsing: downstream nodes can address into it.
	if got := evaluator.GetMsgValByPath(res, "payload.contact.email"); got != "ada@example.com" {
		t.Errorf("nested path payload.contact.email: got %v", got)
	}
	if got := evaluator.GetMsgValByPath(res, "payload.tags.0"); got != "a" {
		t.Errorf("array path payload.tags.0: got %v", got)
	}
}

// TestParseJSONObjectsRendersAsATree is the preview requirement: the value must
// serialise as a nested object, not as one escaped string.
func TestParseJSONObjectsRendersAsATree(t *testing.T) {
	sealed := sealJSON(t, nil)

	res, err := (&DecryptTransformer{}).Transform(t.Context(),
		newMsg(t, map[string]any{"payload": sealed}),
		map[string]any{"field": "payload", "key": "a passphrase", "parseJson": "objects"})
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}

	out, err := json.Marshal(res.Data())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(out), `\"`) {
		t.Fatalf("value is still an escaped string, not an object: %s", out)
	}
	if !strings.Contains(string(out), `"contact":{"email":"ada@example.com"}`) {
		t.Fatalf("expected a nested object, got: %s", out)
	}
}

// TestParseJSONObjectsLeavesScalarsAlone: "objects" means objects and arrays.
// A decrypted "123-45-6789" must stay a string, and a decrypted "12345" must
// not silently become a number — a type change is a downstream schema change.
func TestParseJSONObjectsLeavesScalarsAlone(t *testing.T) {
	for _, plain := range []string{"123-45-6789", "12345", "true", "null", "hello"} {
		t.Run(plain, func(t *testing.T) {
			res, err := (&EncryptTransformer{}).Transform(t.Context(),
				newMsg(t, map[string]any{"v": plain}),
				map[string]any{"field": "v", "key": "a passphrase"})
			if err != nil {
				t.Fatalf("encrypt: %v", err)
			}
			ct, _ := res.Data()["v"].(string)

			out, err := (&DecryptTransformer{}).Transform(t.Context(),
				newMsg(t, map[string]any{"v": ct}),
				map[string]any{"field": "v", "key": "a passphrase", "parseJson": "objects"})
			if err != nil {
				t.Fatalf("decrypt: %v", err)
			}
			if got, ok := out.Data()["v"].(string); !ok || got != plain {
				t.Fatalf("scalar changed type: got %#v, want the string %q", out.Data()["v"], plain)
			}
		})
	}
}

// TestParseJSONObjectsIgnoresBrokenJSON: in "objects" mode a value that looks
// like JSON but is not stays the decrypted string rather than failing. Use
// "strict" when the document must be parseable.
func TestParseJSONObjectsIgnoresBrokenJSON(t *testing.T) {
	const broken = `{"name":"Ada"` // truncated

	res, err := (&EncryptTransformer{}).Transform(t.Context(),
		newMsg(t, map[string]any{"payload": broken}),
		map[string]any{"field": "payload", "key": "a passphrase"})
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	ct, _ := res.Data()["payload"].(string)

	out, err := (&DecryptTransformer{}).Transform(t.Context(),
		newMsg(t, map[string]any{"payload": ct}),
		map[string]any{"field": "payload", "key": "a passphrase", "parseJson": "objects"})
	if err != nil {
		t.Fatalf("objects mode must not fail on unparseable JSON: %v", err)
	}
	if got := out.Data()["payload"]; got != broken {
		t.Fatalf("got %#v, want the decrypted string back", got)
	}
}

// TestParseJSONStrictFailsOnBrokenJSON is the loud counterpart: a node told the
// column holds JSON should say so when it does not, rather than passing a
// string downstream where an object was expected.
func TestParseJSONStrictFailsOnBrokenJSON(t *testing.T) {
	res, err := (&EncryptTransformer{}).Transform(t.Context(),
		newMsg(t, map[string]any{"payload": `{"name":"Ada"`}),
		map[string]any{"field": "payload", "key": "a passphrase"})
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	ct, _ := res.Data()["payload"].(string)

	_, err = (&DecryptTransformer{}).Transform(t.Context(),
		newMsg(t, map[string]any{"payload": ct}),
		map[string]any{"field": "payload", "key": "a passphrase", "parseJson": "strict"})
	if err == nil {
		t.Fatal("strict mode accepted a value that is not JSON")
	}
	if !strings.Contains(err.Error(), "payload") {
		t.Errorf("error should name the field, got: %v", err)
	}
}

// TestParseJSONStrictHonoursOnError: a parse failure is a per-value failure, so
// it takes the same policies as a decryption failure.
func TestParseJSONStrictHonoursOnError(t *testing.T) {
	res, err := (&EncryptTransformer{}).Transform(t.Context(),
		newMsg(t, map[string]any{"payload": `{"name":"Ada"`}),
		map[string]any{"field": "payload", "key": "a passphrase"})
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	ct, _ := res.Data()["payload"].(string)

	t.Run("skip keeps the decrypted string", func(t *testing.T) {
		out, err := (&DecryptTransformer{}).Transform(t.Context(),
			newMsg(t, map[string]any{"payload": ct}),
			map[string]any{"field": "payload", "key": "a passphrase",
				"parseJson": "strict", "onError": "skip"})
		if err != nil {
			t.Fatalf("skip should not error: %v", err)
		}
		if got := out.Data()["payload"]; got != `{"name":"Ada"` {
			t.Fatalf("got %#v, want the decrypted string", got)
		}
	})

	t.Run("null clears it", func(t *testing.T) {
		out, err := (&DecryptTransformer{}).Transform(t.Context(),
			newMsg(t, map[string]any{"payload": ct}),
			map[string]any{"field": "payload", "key": "a passphrase",
				"parseJson": "strict", "onError": "null"})
		if err != nil {
			t.Fatalf("null should not error: %v", err)
		}
		if got := out.Data()["payload"]; got != nil {
			t.Fatalf("got %#v, want nil", got)
		}
	})
}

// TestParseJSONStrictParsesScalars: unlike "objects", strict means the whole
// value is a JSON document, so a bare number really is a number.
func TestParseJSONStrictParsesScalars(t *testing.T) {
	res, err := (&EncryptTransformer{}).Transform(t.Context(),
		newMsg(t, map[string]any{"n": "12345"}),
		map[string]any{"field": "n", "key": "a passphrase"})
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	ct, _ := res.Data()["n"].(string)

	out, err := (&DecryptTransformer{}).Transform(t.Context(),
		newMsg(t, map[string]any{"n": ct}),
		map[string]any{"field": "n", "key": "a passphrase", "parseJson": "strict"})
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if got, ok := out.Data()["n"].(float64); !ok || got != 12345 {
		t.Fatalf("strict should parse a bare number, got %#v", out.Data()["n"])
	}
}

// TestSerializeJSONEncryptsAnObject is the other half of the round trip.
//
// Encrypt refuses composite values by default, because rendering a map with %v
// yields Go syntax that decrypt would hand back as a literal string. Serialising
// to JSON first is the correct way to seal a subtree, and it makes the object
// survive a full encrypt/decrypt cycle.
func TestSerializeJSONEncryptsAnObject(t *testing.T) {
	original := map[string]any{
		"name":    "Ada",
		"contact": map[string]any{"email": "ada@example.com"},
	}

	sealed, err := (&EncryptTransformer{}).Transform(t.Context(),
		newMsg(t, map[string]any{"payload": original}),
		map[string]any{"field": "payload", "key": "a passphrase", "serializeJson": true})
	if err != nil {
		t.Fatalf("encrypt an object with serializeJson: %v", err)
	}
	ct, ok := sealed.Data()["payload"].(string)
	if !ok || !strings.HasPrefix(ct, "enc:") {
		t.Fatalf("expected ciphertext, got %#v", sealed.Data()["payload"])
	}

	out, err := (&DecryptTransformer{}).Transform(t.Context(),
		newMsg(t, map[string]any{"payload": ct}),
		map[string]any{"field": "payload", "key": "a passphrase", "parseJson": "objects"})
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}

	obj, ok := out.Data()["payload"].(map[string]any)
	if !ok {
		t.Fatalf("expected a map back, got %T", out.Data()["payload"])
	}
	if obj["name"] != "Ada" {
		t.Errorf("name: got %v", obj["name"])
	}
	contact, ok := obj["contact"].(map[string]any)
	if !ok || contact["email"] != "ada@example.com" {
		t.Errorf("nested object did not survive: %#v", obj["contact"])
	}
}

// TestObjectStillRefusedWithoutSerializeJSON keeps the existing guard: without
// the opt-in, a composite value is an error rather than Go syntax in a column.
func TestObjectStillRefusedWithoutSerializeJSON(t *testing.T) {
	_, err := (&EncryptTransformer{}).Transform(t.Context(),
		newMsg(t, map[string]any{"payload": map[string]any{"a": 1}}),
		map[string]any{"field": "payload", "key": "a passphrase"})
	if err == nil {
		t.Fatal("an object was encrypted without serializeJson being set")
	}
	if !strings.Contains(err.Error(), "serializeJson") {
		t.Errorf("the error should point at the option that allows it, got: %v", err)
	}
}

// TestUnknownParseJSONModeFails: a typo must not silently mean "off".
func TestUnknownParseJSONModeFails(t *testing.T) {
	_, err := (&DecryptTransformer{}).Transform(t.Context(),
		newMsg(t, map[string]any{"payload": "x"}),
		map[string]any{"field": "payload", "key": "a passphrase", "parseJson": "yes"})
	if err == nil {
		t.Fatal("an unknown parseJson mode was accepted")
	}
	if !strings.Contains(err.Error(), "yes") {
		t.Errorf("the error should name the bad value, got: %v", err)
	}
}
