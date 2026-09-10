package security

import (
	"strings"
	"testing"
)

// The remaining two ways an external AES-GCM value can be framed differently
// from Go's: where the authentication tag sits, and how long the nonce is.
//
// Go's gcm.Seal appends the tag, so a Go-written value is nonce||ciphertext||tag
// with a 12-byte nonce. Node's crypto, Java's Cipher and .NET's AesGcm all hand
// the tag back *separately*, which leaves whoever wrote the storage code to
// decide where it goes — and putting it in front of the ciphertext is a common
// choice. 16-byte nonces turn up for the same reason: nothing stopped them.
//
// The fixtures below were produced by Node's crypto module, not by this
// package, so they check interoperability rather than self-consistency.

const layoutKey = "0123456789abcdef0123456789abcdef"
const layoutPlain = `{"id":"42","email":"someone@example.com"}`

func layoutCfg(extra map[string]any) map[string]any {
	cfg := map[string]any{
		"field": "payload", "key": layoutKey, "keyFormat": "raw",
		"algorithm": "aes-256-gcm", "format": "raw", "encoding": "base64",
	}
	for k, v := range extra {
		cfg[k] = v
	}
	return cfg
}

// TestDecryptNodeTagPrefix reads iv||tag||ciphertext, the layout that failed
// before tagPlacement existed.
func TestDecryptNodeTagPrefix(t *testing.T) {
	const foreign = "MTIzNDU2Nzg5MDEyTXznIPMObKiKgT1lxHCn//J4oJeEQhJqYb0tx2OCYvr6yNNQS5N19SgzYbVPFcYcfd1IvsWL1KId"

	res, err := (&DecryptTransformer{}).Transform(t.Context(),
		newMsg(t, map[string]any{"payload": foreign}),
		layoutCfg(map[string]any{"tagPlacement": "prefix"}))
	if err != nil {
		t.Fatalf("decrypt node-style tag-prefix value: %v", err)
	}
	if got := res.Data()["payload"]; got != layoutPlain {
		t.Fatalf("got %v", got)
	}
}

// TestDecryptNode16ByteNonce reads a value whose nonce is 16 bytes rather than
// the 96 bits GCM is normally used with.
func TestDecryptNode16ByteNonce(t *testing.T) {
	const foreign = "MTIzNDU2Nzg5MDEyMzQ1NqZjm0MdodsdHNtOU9Sk/jmIuCfmNpzyJIKRT5XQ1/WtFnDvMW/R3in3ZqDQJI3TDKo3ZU8EAXisww=="

	res, err := (&DecryptTransformer{}).Transform(t.Context(),
		newMsg(t, map[string]any{"payload": foreign}),
		layoutCfg(map[string]any{"nonceSize": 16}))
	if err != nil {
		t.Fatalf("decrypt 16-byte-nonce value: %v", err)
	}
	if got := res.Data()["payload"]; got != layoutPlain {
		t.Fatalf("got %v", got)
	}
}

// TestDecryptNodeBothQuirks: the two settings have to compose.
func TestDecryptNodeBothQuirks(t *testing.T) {
	const foreign = "MTIzNDU2Nzg5MDEyMzQ1Nmag0CSN0wyqN2VPBAF4rMOmY5tDHaHbHRzbTlPUpP45iLgn5jac8iSCkU+V0Nf1rRZw7zFv0d4p9w=="

	res, err := (&DecryptTransformer{}).Transform(t.Context(),
		newMsg(t, map[string]any{"payload": foreign}),
		layoutCfg(map[string]any{"nonceSize": 16, "tagPlacement": "prefix"}))
	if err != nil {
		t.Fatalf("decrypt 16-byte-nonce tag-prefix value: %v", err)
	}
	if got := res.Data()["payload"]; got != layoutPlain {
		t.Fatalf("got %v", got)
	}
}

// TestTagPlacementRoundTrip: Hermod must also be able to *write* the layout, or
// it can read a partner system's data without being able to reply to it.
func TestTagPlacementRoundTrip(t *testing.T) {
	for _, placement := range []string{"suffix", "prefix"} {
		t.Run(placement, func(t *testing.T) {
			cfg := layoutCfg(map[string]any{"tagPlacement": placement})

			sealed, err := (&EncryptTransformer{}).Transform(t.Context(),
				newMsg(t, map[string]any{"payload": layoutPlain}), cfg)
			if err != nil {
				t.Fatalf("encrypt: %v", err)
			}
			ct, _ := sealed.Data()["payload"].(string)

			out, err := (&DecryptTransformer{}).Transform(t.Context(),
				newMsg(t, map[string]any{"payload": ct}), cfg)
			if err != nil {
				t.Fatalf("decrypt: %v", err)
			}
			if got := out.Data()["payload"]; got != layoutPlain {
				t.Fatalf("round trip: got %v", got)
			}
		})
	}
}

// TestTagPlacementMismatchFails: reading a tag-prefix value as tag-suffix must
// fail rather than return mangled plaintext.
func TestTagPlacementMismatchFails(t *testing.T) {
	sealed, err := (&EncryptTransformer{}).Transform(t.Context(),
		newMsg(t, map[string]any{"payload": layoutPlain}),
		layoutCfg(map[string]any{"tagPlacement": "prefix"}))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	ct, _ := sealed.Data()["payload"].(string)

	if _, err := (&DecryptTransformer{}).Transform(t.Context(),
		newMsg(t, map[string]any{"payload": ct}),
		layoutCfg(nil)); err == nil {
		t.Fatal("a tag-prefix value opened as tag-suffix")
	}
}

// TestNonceSizeRoundTrip covers writing as well as reading.
func TestNonceSizeRoundTrip(t *testing.T) {
	for _, ns := range []int{8, 12, 16} {
		t.Run(string(rune('0'+ns/10))+string(rune('0'+ns%10)), func(t *testing.T) {
			cfg := layoutCfg(map[string]any{"nonceSize": ns})

			sealed, err := (&EncryptTransformer{}).Transform(t.Context(),
				newMsg(t, map[string]any{"payload": layoutPlain}), cfg)
			if err != nil {
				t.Fatalf("encrypt with %d-byte nonce: %v", ns, err)
			}
			out, err := (&DecryptTransformer{}).Transform(t.Context(), sealed, cfg)
			if err != nil {
				t.Fatalf("decrypt with %d-byte nonce: %v", ns, err)
			}
			if got := out.Data()["payload"]; got != layoutPlain {
				t.Fatalf("round trip: got %v", got)
			}
		})
	}
}

// TestNonceSizeMismatchFails: the nonce length decides where the ciphertext
// starts, so a wrong one must not quietly decode from the wrong offset.
func TestNonceSizeMismatchFails(t *testing.T) {
	sealed, err := (&EncryptTransformer{}).Transform(t.Context(),
		newMsg(t, map[string]any{"payload": layoutPlain}),
		layoutCfg(map[string]any{"nonceSize": 16}))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	ct, _ := sealed.Data()["payload"].(string)

	if _, err := (&DecryptTransformer{}).Transform(t.Context(),
		newMsg(t, map[string]any{"payload": ct}),
		layoutCfg(nil)); err == nil {
		t.Fatal("a 16-byte-nonce value opened with a 12-byte nonce")
	}
}

// TestNonceSizeRejectedForChaCha: the Poly1305 constructions have fixed nonce
// lengths, so accepting the setting would silently ignore it.
func TestNonceSizeRejectedForChaCha(t *testing.T) {
	for _, alg := range []string{"chacha20-poly1305", "xchacha20-poly1305"} {
		t.Run(alg, func(t *testing.T) {
			_, err := (&EncryptTransformer{}).Transform(t.Context(),
				newMsg(t, map[string]any{"payload": layoutPlain}),
				layoutCfg(map[string]any{"algorithm": alg, "nonceSize": 16}))
			if err == nil {
				t.Fatalf("%s accepted a custom nonce size", alg)
			}
			if !strings.Contains(err.Error(), "nonceSize") {
				t.Errorf("error should name the setting, got: %v", err)
			}
		})
	}
}

// TestTagPlacementRejectedForUnauthenticated: CBC, CTR and CFB have no tag, so
// the setting is meaningless and accepting it would imply one exists.
func TestTagPlacementRejectedForUnauthenticated(t *testing.T) {
	_, err := (&EncryptTransformer{}).Transform(t.Context(),
		newMsg(t, map[string]any{"payload": layoutPlain}),
		layoutCfg(map[string]any{"algorithm": "aes-256-cbc", "tagPlacement": "prefix"}))
	if err == nil {
		t.Fatal("tagPlacement was accepted for an unauthenticated algorithm")
	}
	if !strings.Contains(err.Error(), "tagPlacement") {
		t.Errorf("error should name the setting, got: %v", err)
	}
}

// TestInvalidSettingsRejected: typos and nonsense must stop the node.
func TestInvalidSettingsRejected(t *testing.T) {
	cases := []struct {
		name  string
		extra map[string]any
		want  string
	}{
		{"unknown tagPlacement", map[string]any{"tagPlacement": "middle"}, "middle"},
		{"zero nonceSize", map[string]any{"nonceSize": 0}, "nonceSize"},
		{"negative nonceSize", map[string]any{"nonceSize": -4}, "nonceSize"},
		{"absurd nonceSize", map[string]any{"nonceSize": 999}, "nonceSize"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := (&EncryptTransformer{}).Transform(t.Context(),
				newMsg(t, map[string]any{"payload": layoutPlain}), layoutCfg(tc.extra))
			if err == nil {
				t.Fatal("accepted an invalid setting")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error should mention %q, got: %v", tc.want, err)
			}
		})
	}
}

// TestLayoutSettingsAreInTheCacheKey: the derived-cipher cache is keyed on the
// configuration, and a nonce size left out of that key would let two nodes
// sharing a passphrase collide and silently use each other's AEAD.
func TestLayoutSettingsAreInTheCacheKey(t *testing.T) {
	a := layoutCfg(map[string]any{"nonceSize": 12})
	b := layoutCfg(map[string]any{"nonceSize": 16})

	sealedA, err := (&EncryptTransformer{}).Transform(t.Context(),
		newMsg(t, map[string]any{"payload": layoutPlain}), a)
	if err != nil {
		t.Fatalf("encrypt a: %v", err)
	}
	sealedB, err := (&EncryptTransformer{}).Transform(t.Context(),
		newMsg(t, map[string]any{"payload": layoutPlain}), b)
	if err != nil {
		t.Fatalf("encrypt b: %v", err)
	}

	outA, err := (&DecryptTransformer{}).Transform(t.Context(), sealedA, a)
	if err != nil {
		t.Fatalf("decrypt a: %v", err)
	}
	outB, err := (&DecryptTransformer{}).Transform(t.Context(), sealedB, b)
	if err != nil {
		t.Fatalf("decrypt b: %v", err)
	}
	if outA.Data()["payload"] != layoutPlain || outB.Data()["payload"] != layoutPlain {
		t.Fatal("cache collision between two nonce sizes sharing a key")
	}
}

// TestEnvelopeStillWorksWithLayoutSettings: the settings describe bytes inside
// the payload, so they must compose with the Hermod envelope too, and a node
// using them must not claim to be writing the legacy enc:v1: shape.
func TestEnvelopeStillWorksWithLayoutSettings(t *testing.T) {
	cfg := map[string]any{
		"field": "payload", "key": "a passphrase",
		"tagPlacement": "prefix", "nonceSize": 16,
	}

	sealed, err := (&EncryptTransformer{}).Transform(t.Context(),
		newMsg(t, map[string]any{"payload": layoutPlain}), cfg)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	ct, _ := sealed.Data()["payload"].(string)
	if strings.HasPrefix(ct, "enc:v1:") {
		t.Fatalf("a non-legacy layout was written as enc:v1:, which older nodes would misread: %q", ct)
	}

	out, err := (&DecryptTransformer{}).Transform(t.Context(), sealed, cfg)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if got := out.Data()["payload"]; got != layoutPlain {
		t.Fatalf("round trip: got %v", got)
	}
}
