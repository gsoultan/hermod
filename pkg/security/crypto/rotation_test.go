package crypto

import (
	"encoding/base64"
	"strings"
	"sync"
	"testing"
)

// ---------------------------------------------------------------------------
// Key rotation.
//
// The master key protects every credential Hermod stores: database passwords,
// API keys, service-account JSON. Rotating it is an operation an admin can
// trigger at any time from PUT /api/config/crypto, on a running system with
// pipelines mid-flight.
//
// Two properties have to hold for that to be safe, and neither was covered.
// ---------------------------------------------------------------------------

// withMasterKey swaps the process-wide key for the duration of a test and puts
// the original back afterwards, so these tests cannot leak into their
// neighbours in this package.
func withMasterKey(t *testing.T, key string) {
	t.Helper()
	saved := state.Load()
	t.Cleanup(func() { state.Store(saved) })
	SetMasterKey(key)
}

// TestRotationIsSafeWhilePipelinesEncrypt is a race test.
//
// SetMasterKey assigns to a package-level variable that Encrypt and Decrypt
// read on every call, with nothing between them. Rotation therefore races
// every running pipeline. Under -race this fails outright; without it, the
// realistic outcome is worse than a crash — a message encrypted with a torn
// view of the key that nothing can decrypt afterwards.
func TestRotationIsSafeWhilePipelinesEncrypt(t *testing.T) {
	withMasterKey(t, strings.Repeat("a", 32))

	var readers sync.WaitGroup
	stop := make(chan struct{})

	// Pipelines encrypting and decrypting credentials, as happens on every
	// source or sink save and every worker start.
	for range 4 {
		readers.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				if enc, err := Encrypt("s3cr3t"); err == nil {
					_, _ = Decrypt(enc)
				}
			}
		})
	}

	// An admin rotating the key underneath them.
	for i := range 50 {
		SetMasterKey(strings.Repeat(string(rune('a'+i%26)), 32))
	}

	close(stop)
	readers.Wait()
}

// TestDecryptUnderARotatedKeyReportsAnError pins the property the storage layer
// depends on to notice that rotation invalidated its data.
//
// Ciphertext written under the old key cannot be read under the new one. That
// is inherent to rotation and not a bug. What matters is that Decrypt says so,
// rather than returning something that looks like a plausible plaintext,
// because the caller's only signal that it must re-encrypt is this error.
func TestDecryptUnderARotatedKeyReportsAnError(t *testing.T) {
	withMasterKey(t, strings.Repeat("a", 32))

	const secret = "hunter2"
	enc, err := Encrypt(secret)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	// The admin rotates.
	SetMasterKey(strings.Repeat("b", 32))

	got, err := Decrypt(enc)
	if err == nil {
		t.Fatalf("Decrypt succeeded under a rotated key and returned %q; "+
			"callers would treat stale ciphertext as a valid credential", got)
	}
	if got == secret {
		t.Fatal("rotation did not change the effective key")
	}
}

// TestShortKeysAreNotZeroPadded covers the key derivation.
//
// The API accepts any key of 16 characters or more, and anything under 32 bytes
// is zero-padded to length. Padding is not derivation: a 16-character key gives
// 16 bytes of entropy followed by 16 known-zero bytes, and — more sharply — two
// different keys sharing a 32-byte prefix derive to the *same* AES key, so
// rotating from one to the other silently does nothing at all.
func TestShortKeysAreNotZeroPadded(t *testing.T) {
	withMasterKey(t, strings.Repeat("x", 40))
	first := derive(strings.Repeat("x", 40))

	// A different key that shares the first 32 characters.
	second := derive(strings.Repeat("x", 32) + "-a-completely-different-suffix")

	if string(first) == string(second) {
		t.Error("two different master keys derived to the same AES key: the key is " +
			"truncated at 32 bytes rather than derived, so an admin rotating between " +
			"them would get a success response and no actual rotation")
	}
}

// TestLegacyCiphertextStillDecrypts is the upgrade guarantee.
//
// Changing the derivation changes the AES key, which would make every
// credential already sitting in a deployment's database unreadable — the exact
// failure this work exists to prevent. Decrypt therefore falls back to the
// historical derivation, so an upgrade is a no-op from the operator's side.
func TestLegacyCiphertextStillDecrypts(t *testing.T) {
	const key = "an-operator-chosen-master-key"
	const secret = "postgres://user:hunter2@db/prod"

	withMasterKey(t, key)

	// Ciphertext exactly as an older build would have written it.
	legacyAEAD, err := newAEAD(deriveLegacy(key))
	if err != nil {
		t.Fatalf("legacy AEAD: %v", err)
	}
	nonce := make([]byte, legacyAEAD.NonceSize())
	for i := range nonce {
		nonce[i] = byte(i)
	}
	legacyCiphertext := base64.StdEncoding.EncodeToString(
		legacyAEAD.Seal(nonce, nonce, []byte(secret), nil))

	got, err := Decrypt(legacyCiphertext)
	if err != nil {
		t.Fatalf("cannot read a credential written by an older build: %v; "+
			"upgrading would lock every deployment out of its own connections", err)
	}
	if got != secret {
		t.Errorf("legacy decrypt returned %q, want %q", got, secret)
	}
}

// TestReEncryptedValueUsesTheCurrentScheme confirms data migrates forward: a
// value rewritten after the upgrade is readable without the legacy fallback.
func TestReEncryptedValueUsesTheCurrentScheme(t *testing.T) {
	const key = "an-operator-chosen-master-key"
	withMasterKey(t, key)

	enc, err := Encrypt("secret")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	// A build with no legacy fallback at all must still read it.
	primaryOnly, err := newAEAD(derive(key))
	if err != nil {
		t.Fatalf("primary AEAD: %v", err)
	}
	state.Store(&keyState{primary: primaryOnly, isKey: key})

	if got, err := Decrypt(enc); err != nil || got != "secret" {
		t.Errorf("newly written value did not use the current derivation: got %q, err %v", got, err)
	}
}
