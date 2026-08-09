package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"sync/atomic"
)

// DefaultMasterKey is the key used when an operator has not configured one. It
// is a constant in published source, so anything encrypted under it is
// protected from nobody; IsDefaultMasterKey exists so start-up can say so.
const DefaultMasterKey = "hermod-default-master-key-32byte"

// keyState holds the ciphers derived from one master key.
//
// Deriving AES-GCM per call was both wasteful and, more importantly, racy: the
// key lived in a plain package variable that SetMasterKey wrote while every
// Encrypt and Decrypt read it. Rotation is an online operation, so that race
// was reachable from a live admin request. The derived state is now swapped
// atomically as a unit.
type keyState struct {
	primary cipher.AEAD // key derived with SHA-256
	legacy  cipher.AEAD // key derived the historical way; decrypt-only
	isKey   string      // the configured key, for IsDefaultMasterKey
}

var state atomic.Pointer[keyState]

func init() { SetMasterKey(DefaultMasterKey) }

// derive turns a configured key of any length into 32 bytes.
//
// The previous scheme truncated at 32 bytes and zero-padded anything shorter.
// Both are wrong in ways that matter: padding a 16-character key (the API
// minimum) leaves half the key known, and truncation means two different keys
// sharing a 32-character prefix derive to the same AES key — so an admin
// rotating between them gets a success response and no rotation at all.
// Hashing uses the whole key and always yields 32 bytes.
func derive(key string) []byte {
	sum := sha256.Sum256([]byte(key))
	return sum[:]
}

// deriveLegacy reproduces the historical derivation exactly.
//
// It has to stay: every credential already in a deployment's database was
// encrypted under it. Decrypt falls back to it so an upgrade does not lock
// operators out of their own data, and each value moves to the current scheme
// the next time it is written.
func deriveLegacy(key string) []byte {
	if len(key) >= 32 {
		return []byte(key[:32])
	}
	padded := make([]byte, 32)
	copy(padded, key)
	return padded
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// SetMasterKey installs key as the master key. An empty key is ignored, so a
// missing configuration leaves the previous key in place rather than silently
// falling back to a weaker one.
//
// Note that rotating the key does not re-encrypt anything: ciphertext written
// under the old key stops being readable the moment this returns. Callers that
// rotate must re-encrypt what they own; see storage's ReEncryptSecrets.
func SetMasterKey(key string) {
	if key == "" {
		return
	}
	primary, err := newAEAD(derive(key))
	if err != nil {
		// derive always returns 32 bytes, so AES-256 cannot reject it.
		return
	}
	next := &keyState{primary: primary, isKey: key}
	if legacy, err := newAEAD(deriveLegacy(key)); err == nil {
		next.legacy = legacy
	}
	state.Store(next)
}

// IsDefaultMasterKey reports whether encryption is running on the built-in key,
// which means stored credentials are readable by anyone with the source.
func IsDefaultMasterKey() bool {
	s := state.Load()
	return s == nil || s.isKey == DefaultMasterKey
}

func Encrypt(text string) (string, error) {
	s := state.Load()
	if s == nil {
		return "", errors.New("crypto: master key is not initialised")
	}

	nonce := make([]byte, s.primary.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	ciphertext := s.primary.Seal(nonce, nonce, []byte(text), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// EncryptWith encrypts using an explicitly supplied key, leaving the process
// key untouched.
//
// Rotation needs this. Re-encrypting by installing the new key first and then
// rewriting rows would leave the process mid-rotation for as long as the
// rewrite takes — every concurrent read would fail — and a failure halfway
// through would strand some rows under each key with no way to tell which.
// Producing the new ciphertext up front means the switch happens once, after
// the data is safely written.
func EncryptWith(key, text string) (string, error) {
	if key == "" {
		return "", errors.New("crypto: empty key")
	}
	aead, err := newAEAD(derive(key))
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(aead.Seal(nonce, nonce, []byte(text), nil)), nil
}

// Decrypt reverses Encrypt. It tries the current derivation first and the
// historical one second, so data written by an older build still opens.
//
// An error here means the ciphertext was written under a different master key.
// Callers must treat that as a failure and never fall back to the raw input:
// handing ciphertext to a database driver as if it were a password turns a
// key-rotation mistake into a stream of confusing authentication errors.
func Decrypt(cryptoText string) (string, error) {
	ciphertext, err := base64.StdEncoding.DecodeString(cryptoText)
	if err != nil {
		return "", err
	}

	s := state.Load()
	if s == nil {
		return "", errors.New("crypto: master key is not initialised")
	}

	for _, aead := range []cipher.AEAD{s.primary, s.legacy} {
		if aead == nil {
			continue
		}
		nonceSize := aead.NonceSize()
		if len(ciphertext) < nonceSize {
			return "", errors.New("ciphertext too short")
		}
		nonce, body := ciphertext[:nonceSize], ciphertext[nonceSize:]
		if plaintext, err := aead.Open(nil, nonce, body, nil); err == nil {
			return string(plaintext), nil
		}
	}

	return "", errors.New("crypto: cannot decrypt with the configured master key")
}

func GenerateToken() string {
	b := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return ""
	}
	return base64.URLEncoding.EncodeToString(b)
}

// ComputeHMAC computes a SHA256 HMAC of the data using the given secret.
func ComputeHMAC(data []byte, secret string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}
