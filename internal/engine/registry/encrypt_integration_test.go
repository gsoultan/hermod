package registry

import (
	"strings"
	"testing"

	"github.com/gsoultan/hermod/pkg/comm/message"
	_ "github.com/gsoultan/hermod/pkg/comm/transformer/security"
)

// TestEngineDispatch_EncryptDecrypt runs the encrypt and decrypt transformations
// through the engine's own dispatch rather than by calling them directly.
//
// The transformers register themselves from an init() in a package the engine
// only imports for its side effect, so their unit tests can pass while a node
// configured in a real workflow resolves to nothing. This covers that seam.
func TestEngineDispatch_EncryptDecrypt(t *testing.T) {
	r := &Registry{}
	ctx := t.Context()

	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	msg.SetID("1")
	msg.SetData("ssn", "123-45-6789")

	cfg := map[string]any{"field": "ssn", "key": "engine test key"}

	out, err := r.applyTransformation(ctx, msg, "encrypt", cfg)
	if err != nil {
		t.Fatalf("engine encrypt: %v", err)
	}
	sealed, _ := out.Data()["ssn"].(string)
	if !strings.HasPrefix(sealed, "enc:v1:") {
		t.Fatalf("engine did not encrypt the field: %q", sealed)
	}

	out, err = r.applyTransformation(ctx, out, "decrypt", cfg)
	if err != nil {
		t.Fatalf("engine decrypt: %v", err)
	}
	if out.Data()["ssn"] != "123-45-6789" {
		t.Fatalf("engine round trip: got %v, want the original value", out.Data()["ssn"])
	}
}

// TestEngineDispatch_DecryptWrongKeyStopsMessage checks that a decryption
// failure surfaces as an error at the engine boundary, so the workflow's error
// path handles it instead of forwarding ciphertext to the sink.
func TestEngineDispatch_DecryptWrongKeyStopsMessage(t *testing.T) {
	r := &Registry{}
	ctx := t.Context()

	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	msg.SetID("1")
	msg.SetData("ssn", "123-45-6789")

	sealed, err := r.applyTransformation(ctx, msg, "encrypt", map[string]any{
		"field": "ssn", "key": "the right key",
	})
	if err != nil {
		t.Fatalf("engine encrypt: %v", err)
	}

	if _, err := r.applyTransformation(ctx, sealed, "decrypt", map[string]any{
		"field": "ssn", "key": "the wrong key",
	}); err == nil {
		t.Fatal("expected the engine to surface a decryption failure")
	}
}
