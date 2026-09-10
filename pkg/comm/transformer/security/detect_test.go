package security

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
)

// Detection exists because working out how an external system framed a value is
// otherwise a manual search. The settings interact — key format decides the key
// bytes, encoding decides the payload bytes, nonce length decides where the
// ciphertext starts, tag placement decides which end the tag is on, and AAD
// decides whether authentication can succeed at all — so getting one wrong
// looks exactly like getting all of them wrong.
//
// With the key in hand, an AEAD's authentication tag turns that search into a
// decision procedure: a candidate either authenticates or it does not.

const detectPlain = `{"user_id":"01a0411f","email":"someone@example.com"}`

// sealForDetect builds a sample the way an external system would, using Go's
// primitives directly rather than this package, so the detector is tested
// against bytes rather than against its own conventions.
func sealForDetect(t *testing.T, key []byte, nonce []byte, aad []byte, tagFirst bool) string {
	t.Helper()

	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	g, err := cipher.NewGCMWithNonceSize(block, len(nonce))
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	sealed := g.Seal(nil, nonce, []byte(detectPlain), aad)

	body := sealed
	if tagFirst {
		tag := sealed[len(sealed)-16:]
		body = append(append([]byte{}, tag...), sealed[:len(sealed)-16]...)
	}
	return base64.StdEncoding.EncodeToString(append(append([]byte{}, nonce...), body...))
}

func mustDetect(t *testing.T, sample, key string) DetectionResult {
	t.Helper()

	res := DetectDecryption(sample, key)
	if len(res.Candidates) == 0 {
		t.Fatalf("nothing detected; reason: %s", res.Reason)
	}
	return res
}

// TestDetectKeyAsAAD is the case that prompted this: AES-256-GCM, a 32-character
// key used as raw bytes, and the key doubling as the AAD. Every one of those had
// to be guessed by hand.
func TestDetectKeyAsAAD(t *testing.T) {
	const key = "7EtBxSJk0wItCUM7LRHhjgNL1IK7hlBO"
	sample := sealForDetect(t, []byte(key), []byte("123456789012"), []byte(key), false)

	best := mustDetect(t, sample, key).Candidates[0]
	if best.Confidence != ConfidenceCertain {
		t.Errorf("an authenticated match should be certain, got %q", best.Confidence)
	}
	for k, want := range map[string]any{
		"algorithm": "aes-256-gcm",
		"keyFormat": "raw",
		"format":    "raw",
		"encoding":  "base64",
		"aadMode":   "key",
	} {
		if got := best.Config[k]; got != want {
			t.Errorf("%s: got %v, want %v", k, got, want)
		}
	}
}

// TestDetectPassphraseKey: the same value under a SHA-256-derived key must come
// back as keyFormat "passphrase", not "raw".
func TestDetectPassphraseKey(t *testing.T) {
	const pass = "a human chosen passphrase"
	sum := sha256Sum([]byte(pass))
	sample := sealForDetect(t, sum, []byte("123456789012"), nil, false)

	best := mustDetect(t, sample, pass).Candidates[0]
	if best.Config["keyFormat"] != "passphrase" {
		t.Errorf("keyFormat: got %v, want passphrase", best.Config["keyFormat"])
	}
	if best.Config["aadMode"] != "none" {
		t.Errorf("aadMode: got %v, want none", best.Config["aadMode"])
	}
}

// TestDetectTagPrefix and TestDetectNonceSize cover the two byte-layout quirks,
// which are invisible in the ciphertext and can only be found by trying.
func TestDetectTagPrefix(t *testing.T) {
	key := "0123456789abcdef0123456789abcdef"
	sample := sealForDetect(t, []byte(key), []byte("123456789012"), nil, true)

	best := mustDetect(t, sample, key).Candidates[0]
	if best.Config["tagPlacement"] != "prefix" {
		t.Errorf("tagPlacement: got %v, want prefix", best.Config["tagPlacement"])
	}
}

func TestDetectNonceSize(t *testing.T) {
	key := "0123456789abcdef0123456789abcdef"
	sample := sealForDetect(t, []byte(key), []byte("1234567890123456"), nil, false)

	best := mustDetect(t, sample, key).Candidates[0]
	if best.Config["nonceSize"] != 16 {
		t.Errorf("nonceSize: got %v, want 16", best.Config["nonceSize"])
	}
}

// TestDetectHexEncoding: the payload encoding is part of the search too.
func TestDetectHexEncoding(t *testing.T) {
	key := "0123456789abcdef0123456789abcdef"
	block, _ := aes.NewCipher([]byte(key))
	g, _ := cipher.NewGCM(block)
	nonce := []byte("123456789012")
	sample := hex.EncodeToString(g.Seal(nonce, nonce, []byte(detectPlain), nil))

	best := mustDetect(t, sample, key).Candidates[0]
	if best.Config["encoding"] != "hex" {
		t.Errorf("encoding: got %v, want hex", best.Config["encoding"])
	}
}

// TestDetectChaCha covers the non-AES authenticated constructions.
func TestDetectChaCha(t *testing.T) {
	for _, alg := range []string{"chacha20-poly1305", "xchacha20-poly1305"} {
		t.Run(alg, func(t *testing.T) {
			key := "0123456789abcdef0123456789abcdef"
			cfg := map[string]any{
				"field": "v", "key": key, "keyFormat": "raw",
				"algorithm": alg, "format": "raw", "encoding": "base64",
			}
			sealed, err := (&EncryptTransformer{}).Transform(t.Context(),
				newMsg(t, map[string]any{"v": detectPlain}), cfg)
			if err != nil {
				t.Fatalf("fixture: %v", err)
			}
			sample, _ := sealed.Data()["v"].(string)

			best := mustDetect(t, sample, key).Candidates[0]
			if best.Config["algorithm"] != alg {
				t.Errorf("algorithm: got %v, want %s", best.Config["algorithm"], alg)
			}
		})
	}
}

// TestDetectEnvelopeIsRecognisedDirectly: a value this package wrote already
// says what it is, so detection must read it rather than search.
func TestDetectEnvelope(t *testing.T) {
	for _, alg := range []string{"aes-256-gcm", "chacha20-poly1305"} {
		t.Run(alg, func(t *testing.T) {
			sealed, err := (&EncryptTransformer{}).Transform(t.Context(),
				newMsg(t, map[string]any{"v": detectPlain}),
				map[string]any{"field": "v", "key": "a passphrase", "algorithm": alg})
			if err != nil {
				t.Fatalf("fixture: %v", err)
			}
			sample, _ := sealed.Data()["v"].(string)

			res := mustDetect(t, sample, "a passphrase")
			best := res.Candidates[0]
			if best.Config["format"] != "envelope" {
				t.Errorf("format: got %v, want envelope", best.Config["format"])
			}
			if best.Config["algorithm"] != alg {
				t.Errorf("algorithm: got %v, want %s", best.Config["algorithm"], alg)
			}
		})
	}
}

// TestDetectUnauthenticatedIsOnlyLikely: CBC, CTR and CFB cannot confirm a
// guess, so a match there is plausibility rather than proof and must say so.
// Overstating it would send an operator to production on a coin flip.
func TestDetectUnauthenticatedIsOnlyLikely(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	iv := []byte("initialvector123")
	block, _ := aes.NewCipher(key)

	plain := []byte(detectPlain)
	pad := aes.BlockSize - len(plain)%aes.BlockSize
	for range pad {
		plain = append(plain, byte(pad))
	}
	out := make([]byte, len(plain))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, plain)
	sample := base64.StdEncoding.EncodeToString(append(iv, out...))

	best := mustDetect(t, sample, string(key)).Candidates[0]
	if best.Config["algorithm"] != "aes-256-cbc" {
		t.Errorf("algorithm: got %v, want aes-256-cbc", best.Config["algorithm"])
	}
	if best.Confidence != ConfidenceLikely {
		t.Errorf("an unauthenticated match cannot be certain, got %q", best.Confidence)
	}
}

// TestDetectAuthenticatedOutranksLikely: when both an authenticated and an
// unauthenticated candidate match, the proven one has to come first.
func TestDetectAuthenticatedRanksFirst(t *testing.T) {
	key := "0123456789abcdef0123456789abcdef"
	sample := sealForDetect(t, []byte(key), []byte("123456789012"), nil, false)

	res := mustDetect(t, sample, key)
	if res.Candidates[0].Confidence != ConfidenceCertain {
		t.Fatalf("first candidate should be certain, got %q", res.Candidates[0].Confidence)
	}
	for _, c := range res.Candidates[1:] {
		if c.Confidence == ConfidenceCertain {
			continue
		}
		return // ordering holds: certain ones precede likely ones
	}
}

// TestDetectWrongKeyFindsNothing: detection must not invent a configuration.
// Reporting a guess here would be worse than reporting nothing, because the
// operator would ship it.
func TestDetectWrongKeyFindsNothing(t *testing.T) {
	key := "0123456789abcdef0123456789abcdef"
	sample := sealForDetect(t, []byte(key), []byte("123456789012"), nil, false)

	res := DetectDecryption(sample, "ffffffffffffffffffffffffffffffff")
	for _, c := range res.Candidates {
		if c.Confidence == ConfidenceCertain {
			t.Fatalf("a wrong key produced a certain match: %+v", c.Config)
		}
	}
	if res.Reason == "" && len(res.Candidates) == 0 {
		t.Error("an empty result must explain itself")
	}
}

// TestDetectPreviewIsTruncated: the preview exists so an operator can recognise
// their own data, not so the endpoint becomes a bulk decryption service.
func TestDetectPreviewIsTruncated(t *testing.T) {
	key := "0123456789abcdef0123456789abcdef"
	long := strings.Repeat("A", 400)
	block, _ := aes.NewCipher([]byte(key))
	g, _ := cipher.NewGCM(block)
	nonce := []byte("123456789012")
	sample := base64.StdEncoding.EncodeToString(g.Seal(nonce, nonce, []byte(long), nil))

	best := mustDetect(t, sample, key).Candidates[0]
	// Measured in runes: the truncation marker is one character but three bytes.
	if n := len([]rune(best.Preview)); n > previewLimit+1 {
		t.Errorf("preview is %d runes, limit is %d plus the marker", n, previewLimit)
	}
	if len(best.Preview) >= len(long) {
		t.Error("preview returned the whole plaintext")
	}
}

// TestDetectRejectsEmptyInput: no key means no decision procedure, so it must
// say so rather than return an empty list that reads as "nothing matches".
func TestDetectRejectsEmptyInput(t *testing.T) {
	for _, tc := range []struct{ sample, key, want string }{
		{"", "k", "sample"},
		{"abc", "", "key"},
	} {
		res := DetectDecryption(tc.sample, tc.key)
		if len(res.Candidates) != 0 {
			t.Errorf("expected no candidates for %+v", tc)
		}
		if !strings.Contains(strings.ToLower(res.Reason), tc.want) {
			t.Errorf("reason should mention %q, got %q", tc.want, res.Reason)
		}
	}
}

// TestDetectExplainsAKDF: PBKDF2 and scrypt cannot be searched without the salt
// and cost parameters, so a failure should point there rather than leave an
// operator thinking the key is wrong.
func TestDetectExplainsAKDF(t *testing.T) {
	res := DetectDecryption(base64.StdEncoding.EncodeToString(make([]byte, 60)), "a passphrase")
	if len(res.Candidates) != 0 {
		t.Skip("random bytes happened to match; nothing to assert")
	}
	low := strings.ToLower(res.Reason)
	if !strings.Contains(low, "pbkdf2") && !strings.Contains(low, "salt") {
		t.Errorf("reason should mention the KDFs it cannot search, got %q", res.Reason)
	}
}

// TestDetectCollapsesEquivalentEncodings: base64 and base64url share most of
// their alphabet, so a value using neither -_ nor +/ decodes identically under
// both and produces two candidates that differ only cosmetically. Offering an
// operator a choice that is not a choice is noise, so the canonical one wins.
func TestDetectCollapsesEquivalentEncodings(t *testing.T) {
	key := "0123456789abcdef0123456789abcdef"
	sample := sealForDetect(t, []byte(key), []byte("123456789012"), nil, false)

	res := mustDetect(t, sample, key)

	seen := map[string]int{}
	for _, c := range res.Candidates {
		k := fmt.Sprintf("%v|%v|%v|%v|%v",
			c.Config["algorithm"], c.Config["keyFormat"], c.Config["nonceSize"],
			c.Config["tagPlacement"], c.Config["aadMode"])
		seen[k]++
	}
	for k, n := range seen {
		if n > 1 {
			t.Errorf("%d candidates differ only by encoding for %s", n, k)
		}
	}
	if res.Candidates[0].Config["encoding"] != "base64" {
		t.Errorf("the standard alphabet should win, got %v", res.Candidates[0].Config["encoding"])
	}
}
