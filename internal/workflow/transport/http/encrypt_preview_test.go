package http

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/engine/registry"
	_ "github.com/gsoultan/hermod/pkg/comm/transformer/security"
)

// These tests drive POST /api/transformations/test, which is what the editor's
// Test button calls.
//
// The layer matters. Everything below it has already been covered by the
// transformer's own tests and the engine dispatch tests; what only this layer
// exercises is the JSON decode of the node config as it arrives from a browser.
// Numbers reach the transformer as float64 and field lists as []any, so a
// config reader that type-asserts to int or []string sees nothing here and
// silently falls back to a default the operator never chose.

func postTransformation(t *testing.T, cfg map[string]any, transType string, msg map[string]any) map[string]any {
	t.Helper()

	h := &WorkflowHandler{Handler: &handlers.Handler{Registry: &registry.Registry{}}}

	body, err := json.Marshal(map[string]any{
		"transformation": map[string]any{"type": transType, "config": cfg},
		"message":        msg,
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/transformations/test", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.TestTransformation(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/transformations/test returned %d: %s", rec.Code, rec.Body.String())
	}

	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response %q: %v", rec.Body.String(), err)
	}
	return out
}

// previewedField digs a transformed value out of the preview response, which
// wraps the message in either "result" or "results" depending on the path.
func previewedField(t *testing.T, resp map[string]any, field string) any {
	t.Helper()

	candidates := []any{resp["result"], resp["results"], resp}
	for _, c := range candidates {
		switch v := c.(type) {
		case map[string]any:
			if got, ok := v[field]; ok {
				return got
			}
			if data, ok := v["data"].(map[string]any); ok {
				if got, ok := data[field]; ok {
					return got
				}
			}
		case []any:
			if len(v) > 0 {
				if m, ok := v[0].(map[string]any); ok {
					if got, ok := m[field]; ok {
						return got
					}
					if data, ok := m["data"].(map[string]any); ok {
						if got, ok := data[field]; ok {
							return got
						}
					}
				}
			}
		}
	}
	t.Fatalf("field %q not found in preview response: %v", field, resp)
	return nil
}

// TestPreview_EncryptDecryptOverHTTP is the default configuration end to end.
func TestPreview_EncryptDecryptOverHTTP(t *testing.T) {
	cfg := map[string]any{
		"transType": "encrypt",
		"fields":    []string{"ssn"},
		"key":       "preview key",
	}

	resp := postTransformation(t, cfg, "encrypt", map[string]any{"ssn": "123-45-6789"})
	sealed, _ := previewedField(t, resp, "ssn").(string)
	if !strings.HasPrefix(sealed, "enc:v1:") {
		t.Fatalf("encrypt preview did not encrypt: %q", sealed)
	}

	cfg["transType"] = "decrypt"
	resp = postTransformation(t, cfg, "decrypt", map[string]any{"ssn": sealed})
	if got := previewedField(t, resp, "ssn"); got != "123-45-6789" {
		t.Fatalf("decrypt preview: got %v, want 123-45-6789", got)
	}
}

// TestPreview_AlgorithmPickerOverHTTP walks the picker through the browser
// path, including the numeric KDF parameters that arrive as JSON floats.
func TestPreview_AlgorithmPickerOverHTTP(t *testing.T) {
	cases := []struct {
		name string
		cfg  map[string]any
	}{
		{"chacha20-poly1305", map[string]any{
			"fields": []string{"ssn"}, "key": "preview key",
			"algorithm": "chacha20-poly1305",
		}},
		{"xchacha20 with aad", map[string]any{
			"fields": []string{"ssn"}, "key": "preview key",
			"algorithm": "xchacha20-poly1305", "aad": "tenant-42",
		}},
		{"aes-256-cbc hex raw", map[string]any{
			"fields": []string{"ssn"}, "key": "0123456789abcdef0123456789abcdef",
			"keyFormat": "raw", "algorithm": "aes-256-cbc",
			"format": "raw", "encoding": "hex",
		}},
		{"aes-256-ctr base64url", map[string]any{
			"fields": []string{"ssn"}, "key": "preview key",
			"algorithm": "aes-256-ctr", "encoding": "base64url",
		}},
		{"pbkdf2 floats", map[string]any{
			"fields": []string{"ssn"}, "key": "preview key",
			"keyFormat": "pbkdf2", "kdfSalt": "salty", "kdfIterations": 1000,
		}},
		{"scrypt floats", map[string]any{
			"fields": []string{"ssn"}, "key": "preview key",
			"keyFormat": "scrypt", "kdfSalt": "salty",
			"scryptN": 1024, "scryptR": 8, "scryptP": 1,
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			enc := map[string]any{"transType": "encrypt"}
			dec := map[string]any{"transType": "decrypt"}
			for k, v := range tc.cfg {
				enc[k], dec[k] = v, v
			}

			resp := postTransformation(t, enc, "encrypt", map[string]any{"ssn": "123-45-6789"})
			sealed, _ := previewedField(t, resp, "ssn").(string)
			if sealed == "123-45-6789" || sealed == "" {
				t.Fatalf("encrypt preview did not encrypt: %q", sealed)
			}

			resp = postTransformation(t, dec, "decrypt", map[string]any{"ssn": sealed})
			if got := previewedField(t, resp, "ssn"); got != "123-45-6789" {
				t.Fatalf("decrypt preview: got %v, want 123-45-6789", got)
			}
		})
	}
}

// TestPreview_ForeignCiphertextOverHTTP is the reported bug, driven through the
// editor's own Test button: a column encrypted by another system, decrypted by
// a raw-format node.
func TestPreview_ForeignCiphertextOverHTTP(t *testing.T) {
	// This literal was produced by OpenSSL, not by Hermod:
	//
	//	{ printf 'initialvector123'
	//	  printf '123-45-6789' | openssl enc -aes-256-cbc \
	//	    -K 3031323334353637383961626364656630313233343536373839616263646566 \
	//	    -iv 696e697469616c766563746f72313233
	//	} | openssl base64 -A
	//
	// which is base64(iv || AES-256-CBC(PKCS#7("123-45-6789"))) — the shape an
	// application storing an encrypted column actually writes. Checking against
	// an independent implementation is the point: a fixture generated by the
	// code under test would still pass if both sides shared the same mistake.
	const key = "0123456789abcdef0123456789abcdef"
	const foreign = "aW5pdGlhbHZlY3RvcjEyM0kt74dN5rL4AWXK8XAUy+g="

	resp := postTransformation(t, map[string]any{
		"transType": "decrypt",
		"fields":    []string{"ssn"},
		"key":       key,
		"keyFormat": "raw",
		"algorithm": "aes-256-cbc",
		"format":    "raw",
		"encoding":  "base64",
	}, "decrypt", map[string]any{"ssn": foreign})

	if got := previewedField(t, resp, "ssn"); got != "123-45-6789" {
		t.Fatalf("foreign ciphertext over HTTP: got %v, want 123-45-6789", got)
	}
}

// TestPreview_MisconfiguredNodeReportsAnError checks that a bad configuration
// reaches the operator as a 500 with a readable message rather than a preview
// that quietly shows the input unchanged.
func TestPreview_MisconfiguredNodeReportsAnError(t *testing.T) {
	h := &WorkflowHandler{Handler: &handlers.Handler{Registry: &registry.Registry{}}}

	body, err := json.Marshal(map[string]any{
		"transformation": map[string]any{"type": "decrypt", "config": map[string]any{
			"transType": "decrypt",
			"fields":    []string{"ssn"},
			"key":       "preview key",
			"algorithm": "aes-256-xyz",
		}},
		"message": map[string]any{"ssn": "123-45-6789"},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/transformations/test", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.TestTransformation(rec, req)

	if rec.Code == http.StatusOK {
		t.Fatalf("an unknown algorithm previewed successfully: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "aes-256-xyz") {
		t.Errorf("the error should name the unknown algorithm, got: %s", rec.Body.String())
	}
}

// TestPreview_DecryptedJSONRendersAsAnObject covers the live preview.
//
// The preview panel renders whatever the endpoint returns with
// JSON.stringify(value, null, 2). A decrypted JSON *string* therefore shows up
// as a single escaped line, and no downstream node can address into it. With
// parseJson the endpoint must hand back a real nested object.
func TestPreview_DecryptedJSONRendersAsAnObject(t *testing.T) {
	const doc = `{"name":"Ada","contact":{"email":"ada@example.com"},"tags":["a","b"]}`

	sealedResp := postTransformation(t, map[string]any{
		"transType": "encrypt",
		"fields":    []string{"payload"},
		"key":       "preview key",
	}, "encrypt", map[string]any{"payload": doc})
	sealed, _ := previewedField(t, sealedResp, "payload").(string)
	if !strings.HasPrefix(sealed, "enc:") {
		t.Fatalf("encrypt preview did not encrypt: %q", sealed)
	}

	t.Run("without parseJson it is a string", func(t *testing.T) {
		resp := postTransformation(t, map[string]any{
			"transType": "decrypt",
			"fields":    []string{"payload"},
			"key":       "preview key",
		}, "decrypt", map[string]any{"payload": sealed})

		if _, ok := previewedField(t, resp, "payload").(string); !ok {
			t.Fatalf("expected a string, got %T", previewedField(t, resp, "payload"))
		}
	})

	t.Run("with parseJson it is an object", func(t *testing.T) {
		resp := postTransformation(t, map[string]any{
			"transType": "decrypt",
			"fields":    []string{"payload"},
			"key":       "preview key",
			"parseJson": "objects",
		}, "decrypt", map[string]any{"payload": sealed})

		obj, ok := previewedField(t, resp, "payload").(map[string]any)
		if !ok {
			t.Fatalf("expected a nested object, got %T: %#v",
				previewedField(t, resp, "payload"), previewedField(t, resp, "payload"))
		}
		if obj["name"] != "Ada" {
			t.Errorf("name: got %v", obj["name"])
		}
		contact, ok := obj["contact"].(map[string]any)
		if !ok || contact["email"] != "ada@example.com" {
			t.Errorf("nested object did not survive the preview: %#v", obj["contact"])
		}
		if tags, ok := obj["tags"].([]any); !ok || len(tags) != 2 {
			t.Errorf("array did not survive the preview: %#v", obj["tags"])
		}

		// What the preview panel actually renders. An escaped quote here means
		// the operator is still looking at a string.
		rendered, err := json.MarshalIndent(obj, "", "  ")
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if strings.Contains(string(rendered), `\"`) {
			t.Fatalf("preview would render an escaped string, not a tree:\n%s", rendered)
		}
		t.Logf("live preview renders:\n%s", rendered)
	})
}

// TestPreview_EncryptObjectWithSerializeJSON is the inverse through the same
// endpoint: an object field sealed as one JSON document and read back.
func TestPreview_EncryptObjectWithSerializeJSON(t *testing.T) {
	resp := postTransformation(t, map[string]any{
		"transType":     "encrypt",
		"fields":        []string{"payload"},
		"key":           "preview key",
		"serializeJson": true,
	}, "encrypt", map[string]any{
		"payload": map[string]any{"name": "Ada", "contact": map[string]any{"email": "ada@example.com"}},
	})

	sealed, ok := previewedField(t, resp, "payload").(string)
	if !ok || !strings.HasPrefix(sealed, "enc:") {
		t.Fatalf("expected ciphertext, got %#v", previewedField(t, resp, "payload"))
	}

	back := postTransformation(t, map[string]any{
		"transType": "decrypt",
		"fields":    []string{"payload"},
		"key":       "preview key",
		"parseJson": "objects",
	}, "decrypt", map[string]any{"payload": sealed})

	obj, ok := previewedField(t, back, "payload").(map[string]any)
	if !ok {
		t.Fatalf("expected an object back, got %T", previewedField(t, back, "payload"))
	}
	if obj["name"] != "Ada" {
		t.Errorf("round trip lost the object: %#v", obj)
	}
}

// TestPreview_TransTypeFallback covers the handler's other entry shape.
//
// The editor normally sends the resolved type ("decrypt"), but TestTransformation
// also accepts a generic "transformation" and reads the real type out of
// config.transType. A node saved before the editor set data.transType arrives
// that way, and the decrypt options have to survive the second path too.
func TestPreview_TransTypeFallback(t *testing.T) {
	const doc = `{"name":"Ada"}`

	sealedResp := postTransformation(t, map[string]any{
		"transType": "encrypt", "fields": []string{"payload"}, "key": "preview key",
	}, "transformation", map[string]any{"payload": doc})
	sealed, _ := previewedField(t, sealedResp, "payload").(string)
	if !strings.HasPrefix(sealed, "enc:") {
		t.Fatalf("encrypt via the transformation fallback did not encrypt: %q", sealed)
	}

	resp := postTransformation(t, map[string]any{
		"transType": "decrypt", "fields": []string{"payload"},
		"key": "preview key", "parseJson": "objects",
	}, "transformation", map[string]any{"payload": sealed})

	obj, ok := previewedField(t, resp, "payload").(map[string]any)
	if !ok {
		t.Fatalf("expected an object, got %T", previewedField(t, resp, "payload"))
	}
	if obj["name"] != "Ada" {
		t.Fatalf("got %#v", obj)
	}
}
