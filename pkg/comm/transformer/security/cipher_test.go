package security

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
)

// TestLegacyV1CiphertextStillDecrypts pins the enc:v1: wire format.
//
// The literal below was produced by the AES-256-GCM-only implementation that
// shipped in 1.1.0, before the algorithm picker existed. Anything that version
// encrypted is still sitting in a sink somewhere, so this value must keep
// decrypting under the default configuration. If a change to key derivation,
// envelope framing or base64 handling breaks it, every column written by 1.1.0
// becomes unrecoverable. This test is the tripwire.
func TestLegacyV1CiphertextStillDecrypts(t *testing.T) {
	const legacy = "enc:v1:GYw0dBm4QPKZyJb7wPyHqZepSbhbg3l0aaxCZYUDEM6xqM/sBNdJ"

	msg := newMsg(t, map[string]any{"ssn": legacy})
	res, err := (&DecryptTransformer{}).Transform(t.Context(), msg, map[string]any{
		"field": "ssn",
		"key":   "legacy-passphrase",
	})
	if err != nil {
		t.Fatalf("legacy ciphertext no longer decrypts: %v", err)
	}
	if got := res.Data()["ssn"]; got != "123-45-6789" {
		t.Fatalf("legacy round trip: got %v, want 123-45-6789", got)
	}
}

// TestLegacyConfigStillEmitsV1 keeps the default configuration writing the
// exact envelope an older Hermod can read. A mixed-version fleet is normal
// during a rollout, and a node that silently started emitting enc:v2: would
// leave the older nodes passing ciphertext through untouched.
func TestLegacyConfigStillEmitsV1(t *testing.T) {
	msg := newMsg(t, map[string]any{"ssn": "123-45-6789"})
	res, err := (&EncryptTransformer{}).Transform(t.Context(), msg, map[string]any{
		"field": "ssn", "key": "legacy-passphrase",
	})
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	got, _ := res.Data()["ssn"].(string)
	if !strings.HasPrefix(got, "enc:v1:") {
		t.Fatalf("default config must still emit enc:v1:, got %q", got)
	}
}

// TestRoundTripEveryAlgorithm walks the full picker. Each algorithm must
// encrypt to something that is not the plaintext and decrypt back to it.
func TestRoundTripEveryAlgorithm(t *testing.T) {
	for _, alg := range SupportedAlgorithms() {
		t.Run(alg, func(t *testing.T) {
			cfg := map[string]any{"field": "ssn", "key": "a passphrase", "algorithm": alg}

			msg := newMsg(t, map[string]any{"ssn": "123-45-6789"})
			sealed, err := (&EncryptTransformer{}).Transform(t.Context(), msg, cfg)
			if err != nil {
				t.Fatalf("encrypt with %s: %v", alg, err)
			}
			ct, _ := sealed.Data()["ssn"].(string)
			if ct == "123-45-6789" {
				t.Fatalf("%s did not encrypt the value", alg)
			}

			out, err := (&DecryptTransformer{}).Transform(t.Context(), sealed, cfg)
			if err != nil {
				t.Fatalf("decrypt with %s: %v", alg, err)
			}
			if got := out.Data()["ssn"]; got != "123-45-6789" {
				t.Fatalf("%s round trip: got %v, want 123-45-6789", alg, got)
			}
		})
	}
}

// TestRoundTripEveryKeyFormat covers the key-derivation picker. Every format
// must survive a round trip; the KDFs additionally prove that the salt is
// actually used.
func TestRoundTripEveryKeyFormat(t *testing.T) {
	raw32 := "0123456789abcdef0123456789abcdef"

	cases := []struct {
		name string
		cfg  map[string]any
	}{
		{"passphrase", map[string]any{"key": "a passphrase", "keyFormat": "passphrase"}},
		{"raw", map[string]any{"key": raw32, "keyFormat": "raw"}},
		{"hex", map[string]any{"key": hex.EncodeToString([]byte(raw32)), "keyFormat": "hex"}},
		{"base64", map[string]any{"key": base64.StdEncoding.EncodeToString([]byte(raw32)), "keyFormat": "base64"}},
		{"pbkdf2", map[string]any{"key": "a passphrase", "keyFormat": "pbkdf2", "kdfSalt": "some-salt", "kdfIterations": 1000}},
		{"pbkdf2-sha512", map[string]any{"key": "a passphrase", "keyFormat": "pbkdf2", "kdfSalt": "some-salt", "kdfIterations": 1000, "kdfHash": "sha512"}},
		{"scrypt", map[string]any{"key": "a passphrase", "keyFormat": "scrypt", "kdfSalt": "some-salt", "scryptN": 1024}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := map[string]any{"field": "ssn"}
			for k, v := range tc.cfg {
				cfg[k] = v
			}

			msg := newMsg(t, map[string]any{"ssn": "123-45-6789"})
			sealed, err := (&EncryptTransformer{}).Transform(t.Context(), msg, cfg)
			if err != nil {
				t.Fatalf("encrypt: %v", err)
			}
			out, err := (&DecryptTransformer{}).Transform(t.Context(), sealed, cfg)
			if err != nil {
				t.Fatalf("decrypt: %v", err)
			}
			if got := out.Data()["ssn"]; got != "123-45-6789" {
				t.Fatalf("round trip: got %v, want 123-45-6789", got)
			}
		})
	}
}

// TestKDFSaltIsUsed proves the salt reaches the derivation. Without this a
// dropped salt would still round-trip and look correct while producing the same
// key for every tenant.
func TestKDFSaltIsUsed(t *testing.T) {
	base := map[string]any{
		"field": "ssn", "key": "a passphrase", "keyFormat": "pbkdf2",
		"kdfIterations": 1000, "format": "raw",
	}
	seal := func(salt string) string {
		cfg := map[string]any{"kdfSalt": salt}
		for k, v := range base {
			cfg[k] = v
		}
		cfg["ivPlacement"] = "fixed"
		cfg["iv"] = base64.StdEncoding.EncodeToString(make([]byte, 12))
		msg := newMsg(t, map[string]any{"ssn": "123-45-6789"})
		res, err := (&EncryptTransformer{}).Transform(t.Context(), msg, cfg)
		if err != nil {
			t.Fatalf("encrypt: %v", err)
		}
		s, _ := res.Data()["ssn"].(string)
		return s
	}

	if a, b := seal("salt-a"), seal("salt-b"); a == b {
		t.Fatal("changing kdfSalt did not change the ciphertext; the salt is being ignored")
	}
}

// TestRoundTripEveryEncoding covers the payload-encoding picker.
func TestRoundTripEveryEncoding(t *testing.T) {
	for _, enc := range []string{"base64", "base64url", "hex"} {
		t.Run(enc, func(t *testing.T) {
			cfg := map[string]any{"field": "ssn", "key": "a passphrase", "encoding": enc}

			msg := newMsg(t, map[string]any{"ssn": "123-45-6789"})
			sealed, err := (&EncryptTransformer{}).Transform(t.Context(), msg, cfg)
			if err != nil {
				t.Fatalf("encrypt: %v", err)
			}
			out, err := (&DecryptTransformer{}).Transform(t.Context(), sealed, cfg)
			if err != nil {
				t.Fatalf("decrypt: %v", err)
			}
			if got := out.Data()["ssn"]; got != "123-45-6789" {
				t.Fatalf("round trip: got %v, want 123-45-6789", got)
			}
		})
	}
}

// TestDecryptForeignAESCBC is the reported bug.
//
// A value encrypted by another application — AES-256-CBC, PKCS#7 padded, IV
// prepended, base64 encoded, raw 32-byte key — used to be passed through
// unchanged while the node reported success. In raw format it must decrypt.
func TestDecryptForeignAESCBC(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	iv := []byte("initialvector123")

	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("test fixture: %v", err)
	}
	plain := []byte("123-45-6789")
	pad := aes.BlockSize - len(plain)%aes.BlockSize
	for range pad {
		plain = append(plain, byte(pad))
	}
	out := make([]byte, len(plain))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, plain)
	foreign := base64.StdEncoding.EncodeToString(append(iv, out...))

	msg := newMsg(t, map[string]any{"ssn": foreign})
	res, err := (&DecryptTransformer{}).Transform(t.Context(), msg, map[string]any{
		"field":     "ssn",
		"key":       string(key),
		"keyFormat": "raw",
		"algorithm": "aes-256-cbc",
		"format":    "raw",
		"encoding":  "base64",
	})
	if err != nil {
		t.Fatalf("decrypt foreign ciphertext: %v", err)
	}
	if got := res.Data()["ssn"]; got != "123-45-6789" {
		t.Fatalf("foreign ciphertext: got %v, want 123-45-6789", got)
	}
}

// TestDecryptForeignAESGCMHex covers the other common external shape: a hex
// column with the nonce prepended.
func TestDecryptForeignAESGCMHex(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("test fixture: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("test fixture: %v", err)
	}
	nonce := []byte("123456789012")
	foreign := hex.EncodeToString(gcm.Seal(nonce, nonce, []byte("123-45-6789"), nil))

	msg := newMsg(t, map[string]any{"ssn": foreign})
	res, err := (&DecryptTransformer{}).Transform(t.Context(), msg, map[string]any{
		"field": "ssn", "key": string(key), "keyFormat": "raw",
		"algorithm": "aes-256-gcm", "format": "raw", "encoding": "hex",
	})
	if err != nil {
		t.Fatalf("decrypt foreign ciphertext: %v", err)
	}
	if got := res.Data()["ssn"]; got != "123-45-6789" {
		t.Fatalf("foreign ciphertext: got %v, want 123-45-6789", got)
	}
}

// TestRawFormatFailsLoudlyOnGarbage is the other half of the reported bug: in
// raw format there is no envelope to recognise, so a value that cannot be
// decrypted must be an error rather than a silent pass-through.
func TestRawFormatFailsLoudlyOnGarbage(t *testing.T) {
	msg := newMsg(t, map[string]any{"ssn": "not ciphertext at all"})
	_, err := (&DecryptTransformer{}).Transform(t.Context(), msg, map[string]any{
		"field": "ssn", "key": "a passphrase",
		"algorithm": "aes-256-cbc", "format": "raw",
	})
	if err == nil {
		t.Fatal("raw-format decrypt silently accepted a value it could not decrypt")
	}
}

// TestOnPlaintextFail lets an operator turn the envelope pass-through into an
// error once a rollout is complete, so a decrypt node that has quietly stopped
// matching anything is visible instead of silent.
func TestOnPlaintextFail(t *testing.T) {
	msg := newMsg(t, map[string]any{"ssn": "123-45-6789"})
	_, err := (&DecryptTransformer{}).Transform(t.Context(), msg, map[string]any{
		"field": "ssn", "key": "a passphrase", "onPlaintext": "fail",
	})
	if err == nil {
		t.Fatal("onPlaintext=fail did not report an unencrypted value")
	}
	if !strings.Contains(err.Error(), "ssn") {
		t.Errorf("error should name the field, got: %v", err)
	}
}

// TestOnPlaintextPassthroughIsDefault preserves the documented rollout
// behaviour: during a migration a column holds a mix of both.
func TestOnPlaintextPassthroughIsDefault(t *testing.T) {
	msg := newMsg(t, map[string]any{"ssn": "123-45-6789"})
	res, err := (&DecryptTransformer{}).Transform(t.Context(), msg, map[string]any{
		"field": "ssn", "key": "a passphrase",
	})
	if err != nil {
		t.Fatalf("default must pass plaintext through: %v", err)
	}
	if got := res.Data()["ssn"]; got != "123-45-6789" {
		t.Fatalf("plaintext was altered: %v", got)
	}
}

// TestAEADDetectsTampering is the property that separates the authenticated
// algorithms from the rest, and the reason they are the recommended default.
func TestAEADDetectsTampering(t *testing.T) {
	for _, alg := range []string{"aes-256-gcm", "chacha20-poly1305", "xchacha20-poly1305"} {
		t.Run(alg, func(t *testing.T) {
			cfg := map[string]any{"field": "ssn", "key": "a passphrase", "algorithm": alg}

			msg := newMsg(t, map[string]any{"ssn": "123-45-6789"})
			sealed, err := (&EncryptTransformer{}).Transform(t.Context(), msg, cfg)
			if err != nil {
				t.Fatalf("encrypt: %v", err)
			}
			ct, _ := sealed.Data()["ssn"].(string)

			// Flip the last payload character to something else in the alphabet.
			flipped := []byte(ct)
			if flipped[len(flipped)-1] == 'A' {
				flipped[len(flipped)-1] = 'B'
			} else {
				flipped[len(flipped)-1] = 'A'
			}

			bad := newMsg(t, map[string]any{"ssn": string(flipped)})
			if _, err := (&DecryptTransformer{}).Transform(t.Context(), bad, cfg); err == nil {
				t.Fatalf("%s accepted a tampered ciphertext", alg)
			}
		})
	}
}

// TestAADIsAuthenticated proves the additional-authenticated-data field is
// bound to the ciphertext rather than accepted and dropped.
func TestAADIsAuthenticated(t *testing.T) {
	msg := newMsg(t, map[string]any{"ssn": "123-45-6789"})
	sealed, err := (&EncryptTransformer{}).Transform(t.Context(), msg, map[string]any{
		"field": "ssn", "key": "a passphrase", "aad": "tenant-42",
	})
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	if _, err := (&DecryptTransformer{}).Transform(t.Context(), sealed, map[string]any{
		"field": "ssn", "key": "a passphrase", "aad": "tenant-99",
	}); err == nil {
		t.Fatal("ciphertext bound to tenant-42 decrypted under tenant-99")
	}
}

// TestWrongKeyFails covers every algorithm. The authenticated ones must reject
// the ciphertext; the unauthenticated ones cannot detect a wrong key by
// construction, so they are only required not to return the original plaintext.
func TestWrongKeyFails(t *testing.T) {
	for _, alg := range SupportedAlgorithms() {
		t.Run(alg, func(t *testing.T) {
			msg := newMsg(t, map[string]any{"ssn": "123-45-6789"})
			sealed, err := (&EncryptTransformer{}).Transform(t.Context(), msg, map[string]any{
				"field": "ssn", "key": "the right key", "algorithm": alg,
			})
			if err != nil {
				t.Fatalf("encrypt: %v", err)
			}

			out, err := (&DecryptTransformer{}).Transform(t.Context(), sealed, map[string]any{
				"field": "ssn", "key": "the wrong key", "algorithm": alg,
			})
			if err != nil {
				return // rejected, which is the ideal outcome
			}
			if got := out.Data()["ssn"]; got == "123-45-6789" {
				t.Fatalf("%s decrypted to the correct plaintext under the wrong key", alg)
			}
		})
	}
}

// TestUnknownAlgorithmFails: a typo must stop the pipeline rather than fall
// back to a default the operator did not choose.
func TestUnknownAlgorithmFails(t *testing.T) {
	msg := newMsg(t, map[string]any{"ssn": "123-45-6789"})
	_, err := (&EncryptTransformer{}).Transform(t.Context(), msg, map[string]any{
		"field": "ssn", "key": "a passphrase", "algorithm": "aes-256-xyz",
	})
	if err == nil {
		t.Fatal("an unknown algorithm was accepted")
	}
	if !strings.Contains(err.Error(), "aes-256-xyz") {
		t.Errorf("error should name the unknown algorithm, got: %v", err)
	}
}

// TestKeyFormatLengthMismatchFails: a raw key of the wrong length is an
// operator error that must be reported, not silently padded or truncated.
func TestKeyFormatLengthMismatchFails(t *testing.T) {
	msg := newMsg(t, map[string]any{"ssn": "123-45-6789"})
	_, err := (&EncryptTransformer{}).Transform(t.Context(), msg, map[string]any{
		"field": "ssn", "key": "too short", "keyFormat": "raw", "algorithm": "aes-256-gcm",
	})
	if err == nil {
		t.Fatal("a 9-byte raw key was accepted for AES-256")
	}
}

// TestKDFRequiresSalt: PBKDF2 and scrypt without a salt are a configuration
// error. Defaulting to an empty salt would silently weaken every deployment
// that forgot the field.
func TestKDFRequiresSalt(t *testing.T) {
	for _, format := range []string{"pbkdf2", "scrypt"} {
		t.Run(format, func(t *testing.T) {
			msg := newMsg(t, map[string]any{"ssn": "123-45-6789"})
			_, err := (&EncryptTransformer{}).Transform(t.Context(), msg, map[string]any{
				"field": "ssn", "key": "a passphrase", "keyFormat": format,
			})
			if err == nil {
				t.Fatalf("%s was accepted without a salt", format)
			}
		})
	}
}

// TestAlgorithmMismatchIsReported: an enc:v2: envelope names its algorithm, so
// a decrypt node configured for a different one can say so precisely instead of
// failing with a generic authentication error.
func TestAlgorithmMismatchIsReported(t *testing.T) {
	msg := newMsg(t, map[string]any{"ssn": "123-45-6789"})
	sealed, err := (&EncryptTransformer{}).Transform(t.Context(), msg, map[string]any{
		"field": "ssn", "key": "a passphrase", "algorithm": "chacha20-poly1305",
	})
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	_, err = (&DecryptTransformer{}).Transform(t.Context(), sealed, map[string]any{
		"field": "ssn", "key": "a passphrase", "algorithm": "aes-256-gcm",
	})
	if err == nil {
		t.Fatal("a chacha20-poly1305 envelope decrypted under an aes-256-gcm node")
	}
	if !strings.Contains(err.Error(), "chacha20-poly1305") {
		t.Errorf("error should name the algorithm the value was written with, got: %v", err)
	}
}

// TestFixedIVRoundTrip covers the deterministic-IV escape hatch some external
// systems require. It round-trips, and it is the caller's risk to accept.
func TestFixedIVRoundTrip(t *testing.T) {
	cfg := map[string]any{
		"field": "ssn", "key": "a passphrase",
		"algorithm": "aes-256-cbc", "format": "raw",
		"ivPlacement": "fixed",
		"iv":          base64.StdEncoding.EncodeToString([]byte("initialvector123")),
	}

	msg := newMsg(t, map[string]any{"ssn": "123-45-6789"})
	sealed, err := (&EncryptTransformer{}).Transform(t.Context(), msg, cfg)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	out, err := (&DecryptTransformer{}).Transform(t.Context(), sealed, cfg)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if got := out.Data()["ssn"]; got != "123-45-6789" {
		t.Fatalf("fixed-IV round trip: got %v", got)
	}
}

// TestCacheKeyIncludesAlgorithm guards a specific way this could go wrong: the
// derived-key cache is keyed on the configuration, and if the algorithm were
// left out of that key, two nodes sharing a passphrase would collide and the
// second would silently use the first one's cipher.
func TestCacheKeyIncludesAlgorithm(t *testing.T) {
	gcm := map[string]any{"field": "ssn", "key": "shared", "algorithm": "aes-256-gcm"}
	cha := map[string]any{"field": "ssn", "key": "shared", "algorithm": "chacha20-poly1305"}

	a := newMsg(t, map[string]any{"ssn": "123-45-6789"})
	sealedA, err := (&EncryptTransformer{}).Transform(t.Context(), a, gcm)
	if err != nil {
		t.Fatalf("encrypt gcm: %v", err)
	}
	b := newMsg(t, map[string]any{"ssn": "123-45-6789"})
	sealedB, err := (&EncryptTransformer{}).Transform(t.Context(), b, cha)
	if err != nil {
		t.Fatalf("encrypt chacha: %v", err)
	}

	outA, err := (&DecryptTransformer{}).Transform(t.Context(), sealedA, gcm)
	if err != nil {
		t.Fatalf("decrypt gcm: %v", err)
	}
	outB, err := (&DecryptTransformer{}).Transform(t.Context(), sealedB, cha)
	if err != nil {
		t.Fatalf("decrypt chacha: %v", err)
	}
	if outA.Data()["ssn"] != "123-45-6789" || outB.Data()["ssn"] != "123-45-6789" {
		t.Fatal("cache collision between two algorithms sharing a passphrase")
	}
}

// TestUnsetAlgorithmFollowsTheEnvelope is the other half of
// TestAlgorithmMismatchIsReported. An enc:v2: value names its own algorithm, so
// a decrypt node that was never given one should read it rather than guess the
// default and fail. Only an explicit, contradicted choice is an error.
func TestUnsetAlgorithmFollowsTheEnvelope(t *testing.T) {
	msg := newMsg(t, map[string]any{"ssn": "123-45-6789"})
	sealed, err := (&EncryptTransformer{}).Transform(t.Context(), msg, map[string]any{
		"field": "ssn", "key": "a passphrase", "algorithm": "xchacha20-poly1305",
	})
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	// No "algorithm" key at all on the decrypt node.
	out, err := (&DecryptTransformer{}).Transform(t.Context(), sealed, map[string]any{
		"field": "ssn", "key": "a passphrase",
	})
	if err != nil {
		t.Fatalf("decrypt without an algorithm should follow the envelope: %v", err)
	}
	if got := out.Data()["ssn"]; got != "123-45-6789" {
		t.Fatalf("got %v, want 123-45-6789", got)
	}
}

// TestEncryptSkipsAlreadyEncryptedValues keeps a re-run of a workflow from
// encrypting a column twice, which would make the original value unrecoverable
// without knowing how many times the node had run.
func TestEncryptSkipsAlreadyEncryptedValues(t *testing.T) {
	cfg := map[string]any{"field": "ssn", "key": "a passphrase", "algorithm": "chacha20-poly1305"}

	msg := newMsg(t, map[string]any{"ssn": "123-45-6789"})
	once, err := (&EncryptTransformer{}).Transform(t.Context(), msg, cfg)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	first, _ := once.Data()["ssn"].(string)

	twice, err := (&EncryptTransformer{}).Transform(t.Context(), once, cfg)
	if err != nil {
		t.Fatalf("second encrypt: %v", err)
	}
	if got, _ := twice.Data()["ssn"].(string); got != first {
		t.Fatalf("a second pass re-encrypted the value: %q -> %q", first, got)
	}
}

// TestOnErrorPoliciesOnDecryptFailure covers the three failure policies against
// a value that is genuinely undecryptable.
func TestOnErrorPoliciesOnDecryptFailure(t *testing.T) {
	sealedWith := func(key string) string {
		msg := newMsg(t, map[string]any{"ssn": "123-45-6789"})
		res, err := (&EncryptTransformer{}).Transform(t.Context(), msg, map[string]any{"field": "ssn", "key": key})
		if err != nil {
			t.Fatalf("encrypt: %v", err)
		}
		s, _ := res.Data()["ssn"].(string)
		return s
	}
	ct := sealedWith("the right key")

	t.Run("fail", func(t *testing.T) {
		msg := newMsg(t, map[string]any{"ssn": ct})
		if _, err := (&DecryptTransformer{}).Transform(t.Context(), msg, map[string]any{
			"field": "ssn", "key": "the wrong key", "onError": "fail",
		}); err == nil {
			t.Fatal("expected an error")
		}
	})

	t.Run("skip", func(t *testing.T) {
		msg := newMsg(t, map[string]any{"ssn": ct})
		res, err := (&DecryptTransformer{}).Transform(t.Context(), msg, map[string]any{
			"field": "ssn", "key": "the wrong key", "onError": "skip",
		})
		if err != nil {
			t.Fatalf("skip should not error: %v", err)
		}
		if got := res.Data()["ssn"]; got != ct {
			t.Fatalf("skip should leave the value untouched, got %v", got)
		}
	})

	t.Run("null", func(t *testing.T) {
		msg := newMsg(t, map[string]any{"ssn": ct})
		res, err := (&DecryptTransformer{}).Transform(t.Context(), msg, map[string]any{
			"field": "ssn", "key": "the wrong key", "onError": "null",
		})
		if err != nil {
			t.Fatalf("null should not error: %v", err)
		}
		if got := res.Data()["ssn"]; got != nil {
			t.Fatalf("null should clear the value, got %v", got)
		}
	})
}

// TestMixedV1AndV2InOneColumn is the migration an operator actually performs:
// a column already holds enc:v1: values written by 1.1.0, the nodes are moved
// to a new algorithm, and from then on new rows are written as enc:v2:. Both
// have to keep decrypting through the same decrypt node, or the switch strands
// every row written before it.
//
// enc:v1: carries no algorithm and can only mean AES-256-GCM, so it is read
// that way even when the node is explicitly configured for something else. That
// is deliberately unlike an enc:v2: value whose named algorithm contradicts the
// node, which is a real conflict and reported as one.
func TestMixedV1AndV2InOneColumn(t *testing.T) {
	const key = "the shared key"

	// A row written by 1.1.0.
	legacy := newMsg(t, map[string]any{"ssn": "111-11-1111"})
	legacyOut, err := (&EncryptTransformer{}).Transform(t.Context(), legacy, map[string]any{
		"field": "ssn", "key": key,
	})
	if err != nil {
		t.Fatalf("legacy encrypt: %v", err)
	}
	v1, _ := legacyOut.Data()["ssn"].(string)
	if !strings.HasPrefix(v1, "enc:v1:") {
		t.Fatalf("expected an enc:v1: value, got %q", v1)
	}

	// A row written after the switch to ChaCha20-Poly1305.
	modern := newMsg(t, map[string]any{"ssn": "222-22-2222"})
	modernOut, err := (&EncryptTransformer{}).Transform(t.Context(), modern, map[string]any{
		"field": "ssn", "key": key, "algorithm": "chacha20-poly1305",
	})
	if err != nil {
		t.Fatalf("modern encrypt: %v", err)
	}
	v2, _ := modernOut.Data()["ssn"].(string)
	if !strings.HasPrefix(v2, "enc:v2:chacha20-poly1305:") {
		t.Fatalf("expected an enc:v2: chacha value, got %q", v2)
	}

	// One decrypt node, explicitly configured for the new algorithm, reading both.
	decCfg := map[string]any{"field": "ssn", "key": key, "algorithm": "chacha20-poly1305"}

	oldRow := newMsg(t, map[string]any{"ssn": v1})
	got, err := (&DecryptTransformer{}).Transform(t.Context(), oldRow, decCfg)
	if err != nil {
		t.Fatalf("decrypting a 1.1.0 row after switching algorithms: %v", err)
	}
	if v := got.Data()["ssn"]; v != "111-11-1111" {
		t.Fatalf("legacy row: got %v, want 111-11-1111", v)
	}

	newRow := newMsg(t, map[string]any{"ssn": v2})
	got, err = (&DecryptTransformer{}).Transform(t.Context(), newRow, decCfg)
	if err != nil {
		t.Fatalf("decrypting a post-switch row: %v", err)
	}
	if v := got.Data()["ssn"]; v != "222-22-2222" {
		t.Fatalf("new row: got %v, want 222-22-2222", v)
	}
}
