package security

import (
	"strings"
	"testing"
)

// A decrypt node whose field list matches nothing on the message is a
// misconfiguration, not a data variation: the node runs, touches nothing, and
// forwards ciphertext with no error anywhere. That is the same silent-success
// failure onPlaintext exists to prevent, one level up — the value is never
// reached at all, so no value-level policy can see it.
//
// This is the shape an operator hits when a source starts delivering a body
// that is not a JSON object: the field list still names the old column while
// the body now arrives under "payload".
func TestDecryptFailsWhenNoConfiguredFieldMatches(t *testing.T) {
	msg := newMsg(t, map[string]any{"payload": "HLDFGvyxVbG5/WdxqYSk3="})

	_, err := (&DecryptTransformer{}).Transform(t.Context(), msg, map[string]any{
		"fields":    []any{"data"}, // nothing on the message is called this
		"key":       "0123456789abcdef0123456789abcdef",
		"keyFormat": "raw",
		"algorithm": "aes-256-gcm",
		"format":    "raw",
		"encoding":  "base64",
	})
	if err == nil {
		t.Fatal("a field list matching nothing was accepted silently; " +
			"the node forwarded ciphertext untouched and reported success")
	}
	for _, want := range []string{"data", "payload"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should name the configured field and what is available, "+
				"missing %q: %v", want, err)
		}
	}
}

// The escape hatch, for a stream where some messages genuinely carry none of
// the encrypted fields.
func TestDecryptMissingFieldsSkipPolicy(t *testing.T) {
	msg := newMsg(t, map[string]any{"payload": "whatever"})

	res, err := (&DecryptTransformer{}).Transform(t.Context(), msg, map[string]any{
		"fields":         []any{"data"},
		"onMissingField": "skip",
		"key":            "0123456789abcdef0123456789abcdef",
		"keyFormat":      "raw",
		"algorithm":      "aes-256-gcm",
		"format":         "raw",
		"encoding":       "base64",
	})
	if err != nil {
		t.Fatalf("skip policy should pass the message through: %v", err)
	}
	if got := res.Data()["payload"]; got != "whatever" {
		t.Errorf("payload = %v, want it untouched", got)
	}
}

// A partial match is the heterogeneous case, not a misconfiguration: one field
// is present and decrypts, another is absent on this particular message. That
// must stay quiet, or every optional column becomes a failed message.
func TestDecryptPartialMatchIsNotAnError(t *testing.T) {
	key := "0123456789abcdef0123456789abcdef"
	enc, err := (&EncryptTransformer{}).Transform(t.Context(),
		newMsg(t, map[string]any{"ssn": "123-45-6789"}), map[string]any{
			"fields": []any{"ssn"}, "key": key, "keyFormat": "raw",
			"algorithm": "aes-256-gcm",
		})
	if err != nil {
		t.Fatalf("fixture encrypt: %v", err)
	}

	res, err := (&DecryptTransformer{}).Transform(t.Context(), enc, map[string]any{
		"fields": []any{"ssn", "not_on_this_message"},
		"key":    key, "keyFormat": "raw", "algorithm": "aes-256-gcm",
	})
	if err != nil {
		t.Fatalf("a partially matching field list must not fail: %v", err)
	}
	if got := res.Data()["ssn"]; got != "123-45-6789" {
		t.Errorf("ssn = %v, want it decrypted", got)
	}
}

// Encrypt has the identical trap and gets the identical guard.
func TestEncryptFailsWhenNoConfiguredFieldMatches(t *testing.T) {
	msg := newMsg(t, map[string]any{"payload": "plain text"})

	_, err := (&EncryptTransformer{}).Transform(t.Context(), msg, map[string]any{
		"fields":    []any{"ssn"},
		"key":       "0123456789abcdef0123456789abcdef",
		"keyFormat": "raw",
		"algorithm": "aes-256-gcm",
	})
	if err == nil {
		t.Fatal("an encrypt node matching no field reported success having " +
			"encrypted nothing")
	}
}
