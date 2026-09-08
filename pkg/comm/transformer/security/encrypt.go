package security

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/transformer"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
)

func init() {
	transformer.Register("encrypt", &EncryptTransformer{})
	transformer.Register("decrypt", &DecryptTransformer{})
}

// envelopePrefix marks a value produced by EncryptTransformer.
//
// Without a marker the two transformers cannot tell ciphertext from plaintext,
// and both directions break in the same way: re-running a workflow encrypts an
// already-encrypted column a second time, and decrypt cannot distinguish "this
// was never encrypted" from "this is corrupt". The version segment is what lets
// a future scheme be introduced without stranding data written under this one.
const envelopePrefix = "enc:v1:"

// aeadCacheLimit bounds the derived-key cache. Keys come from workflow config,
// so the realistic ceiling is small; the limit exists so a workflow that
// rewrites its key on every deploy cannot grow the map without end. Past the
// limit derivation still succeeds, it just stops being cached.
const aeadCacheLimit = 256

var (
	aeadCache  sync.Map // [32]byte (SHA-256 of the key) -> cipher.AEAD
	aeadCached atomic.Int64
)

var (
	errNoKey    = errors.New("no encryption key configured: set \"key\" on the node")
	errNoFields = errors.New("no fields configured: set \"field\" or \"fields\" on the node")
)

// aeadFor derives an AES-256-GCM AEAD from an arbitrary-length configured key.
//
// The key is hashed rather than truncated or zero-padded. Truncating means two
// keys sharing a 32-character prefix encrypt identically — an operator rotating
// between them would see success and get no rotation — and padding a short key
// leaves the remaining bytes known to an attacker.
func aeadFor(key string) (cipher.AEAD, error) {
	if key == "" {
		return nil, errNoKey
	}

	sum := sha256.Sum256([]byte(key))
	if v, ok := aeadCache.Load(sum); ok {
		if cached, ok := v.(cipher.AEAD); ok {
			return cached, nil
		}
	}

	// sum is always 32 bytes, so AES-256 cannot reject it and GCM cannot reject
	// the resulting block size; the errors are wrapped rather than dropped so a
	// future change to the derivation cannot fail silently.
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return nil, fmt.Errorf("derive cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("derive AEAD: %w", err)
	}

	if aeadCached.Load() < aeadCacheLimit {
		if _, loaded := aeadCache.LoadOrStore(sum, gcm); !loaded {
			aeadCached.Add(1)
		}
	}
	return gcm, nil
}

// seal encrypts plaintext under aead and wraps it in the versioned envelope.
//
// The nonce is 96 random bits. NIST SP 800-38D caps a key used this way at 2^32
// encryptions before collision probability stops being negligible, which a busy
// CDC pipeline can reach; rotating the key resets that budget, at the cost of
// leaving data written under the old key unreadable.
func seal(aead cipher.AEAD, plaintext string) (string, error) {
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("read nonce: %w", err)
	}
	sealed := aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return envelopePrefix + base64.StdEncoding.EncodeToString(sealed), nil
}

// open reverses seal. The caller must have checked the envelope prefix.
//
// GCM authenticates before it decrypts, so a wrong key and a tampered value are
// the same error here by design: the returned message deliberately says nothing
// about which, and never includes the value or the key.
func open(aead cipher.AEAD, envelope string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(envelope, envelopePrefix))
	if err != nil {
		return "", errors.New("ciphertext is not valid base64")
	}

	nonceSize := aead.NonceSize()
	if len(raw) < nonceSize {
		return "", errors.New("ciphertext is too short to contain a nonce")
	}

	plaintext, err := aead.Open(nil, raw[:nonceSize], raw[nonceSize:], nil)
	if err != nil {
		return "", errors.New("authentication failed: wrong key or altered ciphertext")
	}
	return string(plaintext), nil
}

// scalarText renders a field value as the text to encrypt, refusing composite
// values.
//
// Rendering a map with %v yields Go syntax ("map[email:a@b.com]"), and decrypt
// would hand that literal string back in place of the object — a lossy round
// trip that reports success. Refusing is the only honest answer: encrypting a
// subtree needs an envelope that records the value's type, which enc:v1 does
// not have. Name the leaf fields instead.
func scalarText(val any) (string, error) {
	switch v := val.(type) {
	case string:
		return v, nil
	case []byte:
		return string(v), nil
	}

	switch reflect.ValueOf(val).Kind() {
	case reflect.Map, reflect.Slice, reflect.Array:
		return "", fmt.Errorf("value is a %T; only scalar values can be encrypted", val)
	}
	return fmt.Sprintf("%v", val), nil
}

// configuredKey reads the inline key from the node config.
//
// It is read straight from config on every call and never cached, copied into
// the prepared config, or logged: with inline keys the config map is the only
// place key material is meant to live.
func configuredKey(config map[string]any) string {
	key, _ := config["key"].(string)
	return key
}

// configuredFields resolves the target field paths, preferring the list parsed
// once by Prepare.
//
// There is deliberately no "*" wildcard. Mask has one, but masking every field
// degrades a message where encrypting every field destroys it — primary keys,
// operation type and routing columns included. Encryption targets are named.
func configuredFields(config map[string]any) []string {
	if parsed, ok := config["_parsed_fields"].([]string); ok {
		return parsed
	}
	return parseFields(config)
}

// parseFields accepts the shapes a field list arrives in: a JSON array decoded
// as []any, a Go []string, a comma-separated string, or a single "field".
func parseFields(config map[string]any) []string {
	var out []string

	add := func(s string) {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}

	switch v := config["fields"].(type) {
	case []string:
		for _, f := range v {
			add(f)
		}
	case []any:
		for _, f := range v {
			if s, ok := f.(string); ok {
				add(s)
			}
		}
	case string:
		for _, f := range strings.Split(v, ",") {
			add(f)
		}
	}

	if field, ok := config["field"].(string); ok {
		add(field)
	}
	return out
}

// onErrorPolicy governs what a decryption failure does to the message.
type onErrorPolicy string

const (
	// onErrorFail aborts the message. The default: a step asked to decrypt that
	// silently emits ciphertext downstream is worse than a stopped pipeline.
	onErrorFail onErrorPolicy = "fail"
	// onErrorSkip leaves the value as it is.
	onErrorSkip onErrorPolicy = "skip"
	// onErrorNull replaces the value with nil.
	onErrorNull onErrorPolicy = "null"
)

func configuredOnError(config map[string]any) onErrorPolicy {
	policy, _ := config["onError"].(string)
	switch onErrorPolicy(strings.ToLower(strings.TrimSpace(policy))) {
	case onErrorSkip:
		return onErrorSkip
	case onErrorNull:
		return onErrorNull
	default:
		return onErrorFail
	}
}

// prepareFields precomputes the field list shared by both transformers.
//
// Config validation does not belong here: the engine ignores the error Prepare
// returns (registry_workflow.go calls it as `if prepared, err := ...; err == nil`),
// so a misconfigured node would sail past this and only be caught at Transform.
// Transform is therefore where both transformers fail closed.
func prepareFields(config map[string]any) (map[string]any, error) {
	config["_parsed_fields"] = parseFields(config)
	return config, nil
}

// EncryptTransformer encrypts named fields with AES-256-GCM.
//
// Each value gets a fresh random nonce, so encrypting the same plaintext twice
// yields different ciphertexts. That is the safe default, and it means an
// encrypted column cannot be used as a join or lookup key downstream.
type EncryptTransformer struct{}

func (t *EncryptTransformer) Prepare(config map[string]any) (map[string]any, error) {
	return prepareFields(config)
}

func (t *EncryptTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}

	fields := configuredFields(config)
	if len(fields) == 0 {
		return nil, fmt.Errorf("encrypt: %w", errNoFields)
	}

	aead, err := aeadFor(configuredKey(config))
	if err != nil {
		return nil, fmt.Errorf("encrypt: %w", err)
	}

	for _, field := range fields {
		val := evaluator.GetMsgValByPath(msg, field)
		if val == nil {
			// Absent field. Encrypting a value that is not there would mean
			// inventing one, so leave the message shaped as it arrived.
			continue
		}

		plaintext, err := scalarText(val)
		if err != nil {
			return nil, fmt.Errorf("encrypt: field %q: %w", field, err)
		}
		if strings.HasPrefix(plaintext, envelopePrefix) {
			// Already encrypted, by an earlier run or an upstream node.
			continue
		}

		sealed, err := seal(aead, plaintext)
		if err != nil {
			return nil, fmt.Errorf("encrypt: field %q: %w", field, err)
		}
		msg.SetData(field, sealed)
	}

	return msg, nil
}

// DecryptTransformer reverses EncryptTransformer for named fields.
type DecryptTransformer struct{}

func (t *DecryptTransformer) Prepare(config map[string]any) (map[string]any, error) {
	return prepareFields(config)
}

func (t *DecryptTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}

	fields := configuredFields(config)
	if len(fields) == 0 {
		return nil, fmt.Errorf("decrypt: %w", errNoFields)
	}

	aead, err := aeadFor(configuredKey(config))
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}

	onError := configuredOnError(config)

	for _, field := range fields {
		val := evaluator.GetMsgValByPath(msg, field)
		if val == nil {
			continue
		}

		envelope, ok := val.(string)
		if !ok || !strings.HasPrefix(envelope, envelopePrefix) {
			// Not something this package wrote. During a rollout a column holds
			// a mix of both, so an unencrypted value is passed through rather
			// than treated as a failure.
			continue
		}

		plaintext, err := open(aead, envelope)
		if err != nil {
			switch onError {
			case onErrorSkip:
				continue
			case onErrorNull:
				msg.SetData(field, nil)
				continue
			default:
				return nil, fmt.Errorf("decrypt: field %q: %w", field, err)
			}
		}
		msg.SetData(field, plaintext)
	}

	return msg, nil
}
