package security

// crypto/sha1 below is reachable only as the HMAC hash inside PBKDF2, and only
// when an operator selects it to read data another system already wrote —
// .NET's Rfc2898DeriveBytes, WPA2 and iOS backups all default to
// PBKDF2-HMAC-SHA1. SHA-1's collision weakness does not carry over to HMAC-SHA1
// in a KDF, and dropping it would make that data permanently unreadable, which
// is the failure this whole change exists to fix. It is never used to hash,
// sign, or derive anything Hermod itself writes.

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // G505: PBKDF2-HMAC-SHA1, for reading existing data only
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/scrypt"
)

// This file holds everything that turns node configuration into a concrete
// cipher: the algorithm table, key derivation, payload encoding and the
// seal/open primitives. encrypt.go holds the two transformers and the field
// walk; it asks this file for a resolved suite and does not know how any
// individual algorithm works.

// blockMode distinguishes the non-AEAD constructions, which need an explicit
// mode because the block cipher alone does not define one.
type blockMode uint8

const (
	modeAEAD blockMode = iota
	modeCBC
	modeCTR
	modeCFB
)

// algorithmSpec describes one entry in the picker.
//
// keySize is the exact number of key bytes the algorithm takes; it is also what
// the key-derivation step is asked to produce, which is why a raw or hex key of
// the wrong length is rejected rather than adjusted. ivSize is the nonce (AEAD)
// or IV (block mode) length in bytes.
type algorithmSpec struct {
	name    string
	keySize int
	ivSize  int
	mode    blockMode

	// aead is set for modeAEAD only. Constructing it is the expensive part for
	// AES, so a resolved suite keeps the result.
	aead func(key []byte) (cipher.AEAD, error)
}

// authenticated reports whether the algorithm detects tampering.
//
// The unauthenticated modes are here for interoperability with systems that
// already wrote data in them, not because they are a reasonable choice for new
// pipelines: CBC, CTR and CFB will decrypt an altered ciphertext into altered
// plaintext and report success. The UI says so at the point of choosing.
func (s algorithmSpec) authenticated() bool { return s.mode == modeAEAD }

func aesAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("derive cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("derive AEAD: %w", err)
	}
	return gcm, nil
}

// algorithms is the full picker. Names are lowercase and hyphenated, matching
// the strings the UI writes and the OpenSSL-style spelling operators expect to
// see when they are matching an existing system.
var algorithms = map[string]algorithmSpec{
	"aes-128-gcm": {name: "aes-128-gcm", keySize: 16, ivSize: 12, mode: modeAEAD, aead: aesAEAD},
	"aes-192-gcm": {name: "aes-192-gcm", keySize: 24, ivSize: 12, mode: modeAEAD, aead: aesAEAD},
	"aes-256-gcm": {name: "aes-256-gcm", keySize: 32, ivSize: 12, mode: modeAEAD, aead: aesAEAD},

	"chacha20-poly1305":  {name: "chacha20-poly1305", keySize: 32, ivSize: chacha20poly1305.NonceSize, mode: modeAEAD, aead: chacha20poly1305.New},
	"xchacha20-poly1305": {name: "xchacha20-poly1305", keySize: 32, ivSize: chacha20poly1305.NonceSizeX, mode: modeAEAD, aead: chacha20poly1305.NewX},

	"aes-128-cbc": {name: "aes-128-cbc", keySize: 16, ivSize: aes.BlockSize, mode: modeCBC},
	"aes-192-cbc": {name: "aes-192-cbc", keySize: 24, ivSize: aes.BlockSize, mode: modeCBC},
	"aes-256-cbc": {name: "aes-256-cbc", keySize: 32, ivSize: aes.BlockSize, mode: modeCBC},

	"aes-128-ctr": {name: "aes-128-ctr", keySize: 16, ivSize: aes.BlockSize, mode: modeCTR},
	"aes-192-ctr": {name: "aes-192-ctr", keySize: 24, ivSize: aes.BlockSize, mode: modeCTR},
	"aes-256-ctr": {name: "aes-256-ctr", keySize: 32, ivSize: aes.BlockSize, mode: modeCTR},

	"aes-128-cfb": {name: "aes-128-cfb", keySize: 16, ivSize: aes.BlockSize, mode: modeCFB},
	"aes-192-cfb": {name: "aes-192-cfb", keySize: 24, ivSize: aes.BlockSize, mode: modeCFB},
	"aes-256-cfb": {name: "aes-256-cfb", keySize: 32, ivSize: aes.BlockSize, mode: modeCFB},
}

// defaultAlgorithm is what a node that predates the picker used, and what an
// unconfigured node still gets. Changing it would strand data.
const defaultAlgorithm = "aes-256-gcm"

// SupportedAlgorithms returns the algorithm names in a stable order, for the UI
// picker and for tests that need to walk every entry.
func SupportedAlgorithms() []string {
	out := make([]string, 0, len(algorithms))
	for name := range algorithms {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// AlgorithmIsAuthenticated reports whether the named algorithm detects
// tampering. Exported for the API surface that describes the picker to the UI.
func AlgorithmIsAuthenticated(name string) bool {
	spec, ok := algorithms[strings.ToLower(strings.TrimSpace(name))]
	return ok && spec.authenticated()
}

// ---------------------------------------------------------------- encodings

// payloadEncoding is how the iv+ciphertext bytes are rendered as text, because
// the value has to survive in a string column and in JSON.
type payloadEncoding string

const (
	encodingBase64    payloadEncoding = "base64"
	encodingBase64URL payloadEncoding = "base64url"
	encodingHex       payloadEncoding = "hex"
)

func parseEncoding(s string) (payloadEncoding, error) {
	switch payloadEncoding(strings.ToLower(strings.TrimSpace(s))) {
	case "", encodingBase64:
		return encodingBase64, nil
	case encodingBase64URL:
		return encodingBase64URL, nil
	case encodingHex:
		return encodingHex, nil
	default:
		return "", fmt.Errorf("unknown encoding %q: use base64, base64url or hex", s)
	}
}

func (e payloadEncoding) encode(b []byte) string {
	switch e {
	case encodingBase64URL:
		return base64.URLEncoding.EncodeToString(b)
	case encodingHex:
		return hex.EncodeToString(b)
	default:
		return base64.StdEncoding.EncodeToString(b)
	}
}

// decode is deliberately lenient about padding and about the two base64
// alphabets. External systems are inconsistent — Java's Base64.getUrlEncoder()
// strips padding, Python's urlsafe_b64encode keeps it — and rejecting a value
// over that difference would look exactly like a wrong key to an operator.
// The alphabet is still governed by the configured encoding; only padding and,
// for base64, the -_ / +/ pair are tolerated.
func (e payloadEncoding) decode(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	switch e {
	case encodingHex:
		b, err := hex.DecodeString(s)
		if err != nil {
			return nil, errors.New("value is not valid hex")
		}
		return b, nil
	default:
		if b, err := base64.StdEncoding.DecodeString(s); err == nil {
			return b, nil
		}
		if b, err := base64.RawStdEncoding.DecodeString(s); err == nil {
			return b, nil
		}
		if b, err := base64.URLEncoding.DecodeString(s); err == nil {
			return b, nil
		}
		if b, err := base64.RawURLEncoding.DecodeString(s); err == nil {
			return b, nil
		}
		return nil, errors.New("value is not valid base64")
	}
}

// ------------------------------------------------------------ key derivation

// keyFormat says how to turn the configured key string into cipher key bytes.
type keyFormat string

const (
	// keyPassphrase hashes an arbitrary-length secret with SHA-256.
	//
	// This is what the AES-256-GCM-only implementation did, and it stays the
	// default so that data written before the picker existed keeps decrypting.
	// It is a hash, not a KDF: it is fast, which is fine for a high-entropy
	// secret and wrong for a human-chosen password. Use pbkdf2 or scrypt for
	// the latter.
	keyPassphrase keyFormat = "passphrase"
	keyRaw        keyFormat = "raw"
	keyHex        keyFormat = "hex"
	keyBase64     keyFormat = "base64"
	keyPBKDF2     keyFormat = "pbkdf2"
	keyScrypt     keyFormat = "scrypt"
)

// defaultPBKDF2Iterations follows the OWASP 2023 guidance for PBKDF2-HMAC-SHA256.
const defaultPBKDF2Iterations = 600_000

const (
	defaultScryptN = 32768
	defaultScryptR = 8
	defaultScryptP = 1
)

func parseKeyFormat(s string) (keyFormat, error) {
	switch keyFormat(strings.ToLower(strings.TrimSpace(s))) {
	case "", keyPassphrase:
		return keyPassphrase, nil
	case keyRaw:
		return keyRaw, nil
	case keyHex:
		return keyHex, nil
	case keyBase64:
		return keyBase64, nil
	case keyPBKDF2:
		return keyPBKDF2, nil
	case keyScrypt:
		return keyScrypt, nil
	default:
		return "", fmt.Errorf("unknown keyFormat %q: use passphrase, raw, hex, base64, pbkdf2 or scrypt", s)
	}
}

func hashByName(name string) (func() hash.Hash, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "sha256":
		return sha256.New, nil
	case "sha512":
		return sha512.New, nil
	case "sha1":
		// Present because existing systems used it. PBKDF2-HMAC-SHA1 is not
		// broken by SHA-1's collision weakness, but it is not a choice to make
		// for new data.
		return sha1.New, nil
	default:
		return nil, fmt.Errorf("unknown kdfHash %q: use sha256, sha512 or sha1", name)
	}
}

// exactLength enforces the declared key length for the formats that carry raw
// key bytes.
//
// The key is checked rather than padded or truncated: zero-padding a short key
// leaves the remaining bytes known to an attacker, and truncating means two
// keys sharing a prefix encrypt identically, so an operator rotating between
// them would see success and get no rotation.
func (c *cipherConfig) exactLength(b []byte, keySize int, what string) ([]byte, error) {
	if len(b) != keySize {
		return nil, fmt.Errorf(
			"%s key is %d bytes but %s needs exactly %d; "+
				"a key is never padded or truncated to fit",
			what, len(b), c.spec.name, keySize)
	}
	return b, nil
}

// deriveKDFKey runs the two password-based derivations, which are the only
// formats where the salt and cost parameters apply.
func (c *cipherConfig) deriveKDFKey(keySize int) ([]byte, error) {
	if c.kdfSalt == "" {
		return nil, fmt.Errorf("%s needs a salt: set \"kdfSalt\" on the node", c.keyFormat)
	}

	if c.keyFormat == keyScrypt {
		out, err := scrypt.Key([]byte(c.key), []byte(c.kdfSalt), c.scryptN, c.scryptR, c.scryptP, keySize)
		if err != nil {
			return nil, fmt.Errorf("scrypt: %w", err)
		}
		return out, nil
	}

	h, err := hashByName(c.kdfHash)
	if err != nil {
		return nil, err
	}
	out, err := pbkdf2.Key(h, c.key, []byte(c.kdfSalt), c.kdfIterations, keySize)
	if err != nil {
		return nil, fmt.Errorf("pbkdf2: %w", err)
	}
	return out, nil
}

// deriveKey produces exactly keySize bytes, or explains why it cannot.
func (c *cipherConfig) deriveKey(keySize int) ([]byte, error) {
	if c.key == "" {
		return nil, errNoKey
	}

	switch c.keyFormat {
	case keyRaw:
		return c.exactLength([]byte(c.key), keySize, "raw")

	case keyHex:
		b, err := hex.DecodeString(strings.TrimSpace(c.key))
		if err != nil {
			return nil, errors.New("key is not valid hex")
		}
		return c.exactLength(b, keySize, "hex")

	case keyBase64:
		b, err := encodingBase64.decode(c.key)
		if err != nil {
			return nil, errors.New("key is not valid base64")
		}
		return c.exactLength(b, keySize, "base64")

	case keyPBKDF2, keyScrypt:
		return c.deriveKDFKey(keySize)

	default: // keyPassphrase
		// SHA-256 truncated to keySize. Truncating the *digest* is safe in a way
		// that truncating a key is not, and at keySize 32 this is byte-identical
		// to what enc:v1: was written with.
		sum := sha256.Sum256([]byte(c.key))
		return sum[:keySize], nil
	}
}

// ------------------------------------------------------------------ envelope

// envelopePrefixV1 marks the original AES-256-GCM-only format:
// "enc:v1:" + base64(nonce || ciphertext).
//
// It carries no algorithm, so it can only ever mean AES-256-GCM. It is still
// emitted when the node is configured the way that implementation behaved, so a
// mixed-version fleet keeps working during a rollout.
const envelopePrefixV1 = "enc:v1:"

// envelopePrefixV2 marks the self-describing format:
// "enc:v2:" + algorithm + ":" + encoded(iv || ciphertext).
//
// Naming the algorithm is what lets a decrypt node read a value without being
// told how it was written, and what lets a mismatch be reported precisely
// instead of surfacing as a generic authentication failure.
const envelopePrefixV2 = "enc:v2:"

// hasEnvelope reports whether a value was written by this package.
func hasEnvelope(s string) bool {
	return strings.HasPrefix(s, envelopePrefixV1) || strings.HasPrefix(s, envelopePrefixV2)
}

// ------------------------------------------------------------------- config

// outputFormat selects between the self-describing envelope and bare
// ciphertext.
type outputFormat string

const (
	// formatEnvelope prefixes values with enc:v1:/enc:v2:. Encrypt can then skip
	// values that are already encrypted, and decrypt can tell ciphertext from
	// plaintext. This is the default and the right choice for data Hermod owns.
	formatEnvelope outputFormat = "envelope"
	// formatRaw writes bare encoded ciphertext with no marker, for columns that
	// are read or written by another system that would not understand one.
	//
	// The cost is that nothing can distinguish ciphertext from plaintext:
	// encrypt cannot skip an already-encrypted value, so running an encrypt node
	// twice over the same column encrypts it twice.
	formatRaw outputFormat = "raw"
)

func parseFormat(s string) (outputFormat, error) {
	switch outputFormat(strings.ToLower(strings.TrimSpace(s))) {
	case "", formatEnvelope:
		return formatEnvelope, nil
	case formatRaw:
		return formatRaw, nil
	default:
		return "", fmt.Errorf("unknown format %q: use envelope or raw", s)
	}
}

// ivPlacement says where the per-value IV/nonce lives.
type ivPlacement string

const (
	// ivPrefix generates a fresh random IV per value and prepends it to the
	// ciphertext. The default, and the only safe choice for GCM and CTR.
	ivPrefix ivPlacement = "prefix"
	// ivFixed uses one configured IV for every value. Present only because some
	// external systems do this; see resolveCipher for why it is dangerous.
	ivFixed ivPlacement = "fixed"
)

func parseIVPlacement(s string) (ivPlacement, error) {
	switch ivPlacement(strings.ToLower(strings.TrimSpace(s))) {
	case "", ivPrefix:
		return ivPrefix, nil
	case ivFixed:
		return ivFixed, nil
	default:
		return "", fmt.Errorf("unknown ivPlacement %q: use prefix or fixed", s)
	}
}

// cipherConfig is the resolved, validated form of the node's crypto settings.
type cipherConfig struct {
	spec              algorithmSpec
	algorithmExplicit bool

	key       string
	keyFormat keyFormat

	kdfSalt       string
	kdfHash       string
	kdfIterations int
	scryptN       int
	scryptR       int
	scryptP       int

	encoding    payloadEncoding
	format      outputFormat
	ivPlacement ivPlacement
	fixedIV     []byte
	aad         []byte
}

// legacyShaped reports whether these settings describe exactly what the
// pre-picker implementation produced, in which case encrypt keeps emitting the
// enc:v1: envelope so an older node elsewhere in the fleet can still read it.
func (c *cipherConfig) legacyShaped() bool {
	return c.format == formatEnvelope &&
		c.spec.name == defaultAlgorithm &&
		c.keyFormat == keyPassphrase &&
		c.encoding == encodingBase64 &&
		c.ivPlacement == ivPrefix &&
		len(c.aad) == 0
}

func configString(config map[string]any, key string) string {
	switch v := config[key].(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	}
	return ""
}

// configInt reads a number that may have arrived as a JSON float, a Go int, or
// a string typed into a form field.
func configInt(config map[string]any, key string, fallback int) (int, error) {
	switch v := config[key].(type) {
	case nil:
		return fallback, nil
	case int:
		return v, nil
	case int32:
		return int(v), nil
	case int64:
		return int(v), nil
	case float64:
		return int(v), nil
	case float32:
		return int(v), nil
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return fallback, nil
		}
		n, err := strconv.Atoi(s)
		if err != nil {
			return 0, fmt.Errorf("%q must be a whole number, got %q", key, v)
		}
		return n, nil
	default:
		return 0, fmt.Errorf("%q must be a whole number, got %T", key, v)
	}
}

// parseCipherConfig validates the node's crypto settings up front.
//
// Everything is checked here, before any field is touched, so a misconfigured
// node fails on its first message with one clear message rather than producing
// a partly-transformed record.
func parseCipherConfig(config map[string]any) (*cipherConfig, error) {
	c := &cipherConfig{}

	algName := strings.ToLower(strings.TrimSpace(configString(config, "algorithm")))
	c.algorithmExplicit = algName != ""
	if algName == "" {
		algName = defaultAlgorithm
	}
	spec, ok := algorithms[algName]
	if !ok {
		return nil, fmt.Errorf("unknown algorithm %q: supported algorithms are %s",
			algName, strings.Join(SupportedAlgorithms(), ", "))
	}
	c.spec = spec

	c.key = configString(config, "key")
	if c.key == "" {
		return nil, errNoKey
	}

	var err error
	if c.keyFormat, err = parseKeyFormat(configString(config, "keyFormat")); err != nil {
		return nil, err
	}
	if c.encoding, err = parseEncoding(configString(config, "encoding")); err != nil {
		return nil, err
	}
	if c.format, err = parseFormat(configString(config, "format")); err != nil {
		return nil, err
	}
	if c.ivPlacement, err = parseIVPlacement(configString(config, "ivPlacement")); err != nil {
		return nil, err
	}

	if err = c.parseKDFParams(config); err != nil {
		return nil, err
	}
	if err = c.parseAAD(config); err != nil {
		return nil, err
	}
	if err = c.parseFixedIV(config); err != nil {
		return nil, err
	}

	return c, nil
}

// parseKDFParams reads the salt and cost settings.
//
// They are read whatever the key format is, so that switching a node from
// passphrase to pbkdf2 does not silently discard values already typed into the
// form; only deriveKDFKey acts on them.
func (c *cipherConfig) parseKDFParams(config map[string]any) error {
	var err error
	c.kdfSalt = configString(config, "kdfSalt")
	c.kdfHash = configString(config, "kdfHash")

	if c.kdfIterations, err = configInt(config, "kdfIterations", defaultPBKDF2Iterations); err != nil {
		return err
	}
	if c.kdfIterations < 1 {
		return fmt.Errorf("kdfIterations must be at least 1, got %d", c.kdfIterations)
	}
	if c.scryptN, err = configInt(config, "scryptN", defaultScryptN); err != nil {
		return err
	}
	if c.scryptR, err = configInt(config, "scryptR", defaultScryptR); err != nil {
		return err
	}
	if c.scryptP, err = configInt(config, "scryptP", defaultScryptP); err != nil {
		return err
	}
	return nil
}

// parseAAD reads the additional authenticated data, rejecting it for algorithms
// that have no way to authenticate it. Accepting it silently would suggest the
// value was bound to the ciphertext when nothing had been bound at all.
func (c *cipherConfig) parseAAD(config map[string]any) error {
	aad := configString(config, "aad")
	if aad == "" {
		return nil
	}
	if !c.spec.authenticated() {
		return fmt.Errorf(
			"aad is set but %s is not an authenticated algorithm; "+
				"additional authenticated data only exists for gcm and poly1305 modes", c.spec.name)
	}
	c.aad = []byte(aad)
	return nil
}

// parseFixedIV decodes and length-checks the configured IV.
func (c *cipherConfig) parseFixedIV(config map[string]any) error {
	if c.ivPlacement != ivFixed {
		return nil
	}

	ivText := configString(config, "iv")
	if ivText == "" {
		return errors.New("ivPlacement is \"fixed\" but no \"iv\" is set on the node")
	}
	raw, err := c.encoding.decode(ivText)
	if err != nil {
		return fmt.Errorf("iv: %w", err)
	}
	if len(raw) != c.spec.ivSize {
		return fmt.Errorf("iv is %d bytes but %s needs exactly %d",
			len(raw), c.spec.name, c.spec.ivSize)
	}
	c.fixedIV = raw
	return nil
}

// cacheKey is a stable fingerprint of everything that affects the derived key
// and the constructed cipher.
//
// Getting this wrong is a data-corruption bug rather than a performance one:
// if the algorithm or a KDF parameter were left out, two nodes sharing a
// passphrase would collide in the cache and the second would silently encrypt
// under the first one's cipher. The length prefixes stop two different
// configurations from concatenating to the same string.
func (c *cipherConfig) cacheKey() [32]byte {
	var b strings.Builder
	write := func(s string) {
		b.WriteString(strconv.Itoa(len(s)))
		b.WriteByte(':')
		b.WriteString(s)
		b.WriteByte('|')
	}
	write(c.spec.name)
	write(string(c.keyFormat))
	write(c.key)
	write(c.kdfSalt)
	write(c.kdfHash)
	write(strconv.Itoa(c.kdfIterations))
	write(strconv.Itoa(c.scryptN))
	write(strconv.Itoa(c.scryptR))
	write(strconv.Itoa(c.scryptP))
	return sha256.Sum256([]byte(b.String()))
}

// ------------------------------------------------------------- cipher suites

// cipherSuite is a derived key plus the reusable objects built from it.
//
// cipher.AEAD and cipher.Block are safe for concurrent use; cipher.Stream and
// cipher.BlockMode are not, and are therefore constructed per operation from
// the block held here rather than cached.
type cipherSuite struct {
	spec  algorithmSpec
	key   []byte
	aead  cipher.AEAD
	block cipher.Block
}

// suiteCacheLimit bounds the cache. Keys come from workflow config, so the
// realistic ceiling is small; the limit exists so a workflow that rewrites its
// key on every deploy cannot grow the map without end. Past the limit
// derivation still succeeds, it just stops being cached — which matters most
// for scrypt and PBKDF2, where derivation is deliberately expensive.
const suiteCacheLimit = 256

var (
	suiteCache  sync.Map // [32]byte -> *cipherSuite
	suiteCached atomic.Int64
)

// resolveCipher derives the key and builds the reusable cipher objects.
func resolveCipher(c *cipherConfig) (*cipherSuite, error) {
	ck := c.cacheKey()
	if v, ok := suiteCache.Load(ck); ok {
		if suite, ok := v.(*cipherSuite); ok {
			return suite, nil
		}
	}

	key, err := c.deriveKey(c.spec.keySize)
	if err != nil {
		return nil, err
	}

	suite := &cipherSuite{spec: c.spec, key: key}
	if c.spec.mode == modeAEAD {
		if suite.aead, err = c.spec.aead(key); err != nil {
			return nil, err
		}
	} else {
		if suite.block, err = aes.NewCipher(key); err != nil {
			return nil, fmt.Errorf("derive cipher: %w", err)
		}
	}

	if suiteCached.Load() < suiteCacheLimit {
		if _, loaded := suiteCache.LoadOrStore(ck, suite); !loaded {
			suiteCached.Add(1)
		}
	}
	return suite, nil
}

// ------------------------------------------------------------ seal and open

// newIV returns the IV to encrypt this value under.
//
// A fresh random IV per value is the default. The fixed-IV path exists only for
// interoperability and is genuinely unsafe for the stream modes: reusing an IV
// under CTR or GCM with the same key leaks the XOR of two plaintexts, and for
// GCM it also leaks the authentication subkey. It is reachable only when an
// operator sets ivPlacement explicitly, and the UI says this at that point.
func newIV(c *cipherConfig, size int) ([]byte, error) {
	if c.ivPlacement == ivFixed {
		return c.fixedIV, nil
	}
	iv := make([]byte, size)
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return nil, fmt.Errorf("read iv: %w", err)
	}
	return iv, nil
}

// pkcs7Pad appends PKCS#7 padding, which CBC needs because it only operates on
// whole blocks. A full block of padding is added when the input is already
// aligned, so unpadding is never ambiguous.
func pkcs7Pad(b []byte, blockSize int) []byte {
	n := blockSize - len(b)%blockSize
	out := make([]byte, len(b)+n)
	copy(out, b)
	for i := len(b); i < len(out); i++ {
		out[i] = byte(n)
	}
	return out
}

// pkcs7Unpad removes and validates PKCS#7 padding.
//
// The validation matters beyond correctness: CBC is unauthenticated, so this
// check is the only signal that a wrong key was used. It is written in constant
// time because a padding oracle is the classic way CBC is attacked — an
// attacker who can distinguish "bad padding" from "bad plaintext" by timing can
// recover the plaintext without the key.
func pkcs7Unpad(b []byte, blockSize int) ([]byte, error) {
	if len(b) == 0 || len(b)%blockSize != 0 {
		return nil, errors.New("ciphertext is not a whole number of blocks")
	}

	n := int(b[len(b)-1])
	// Fold the range check into the comparison so every input takes the same
	// path; an out-of-range n is turned into a guaranteed mismatch instead of
	// an early return.
	valid := subtle.ConstantTimeLessOrEq(1, n) & subtle.ConstantTimeLessOrEq(n, blockSize)
	check := n
	if valid == 0 {
		check = blockSize
	}
	good := 1
	for i := range check {
		good &= subtle.ConstantTimeByteEq(b[len(b)-1-i], byte(n))
	}
	if valid&good != 1 {
		return nil, errors.New("padding is invalid: wrong key or altered ciphertext")
	}
	return b[:len(b)-n], nil
}

// sealWith encrypts plaintext and returns the encoded payload bytes, which are
// iv||ciphertext when the IV travels with the value and ciphertext alone when
// it is fixed.
func sealWith(c *cipherConfig, suite *cipherSuite, plaintext []byte) ([]byte, error) {
	iv, err := newIV(c, suite.spec.ivSize)
	if err != nil {
		return nil, err
	}

	// prefix is what gets prepended to the ciphertext. With a fixed IV the
	// reader already knows it, so sending it would only waste bytes.
	var prefix []byte
	if c.ivPlacement == ivPrefix {
		prefix = iv
	}

	switch suite.spec.mode {
	case modeAEAD:
		return suite.aead.Seal(prefix, iv, plaintext, c.aad), nil

	case modeCBC:
		padded := pkcs7Pad(plaintext, aes.BlockSize)
		out := make([]byte, len(padded))
		cipher.NewCBCEncrypter(suite.block, iv).CryptBlocks(out, padded)
		return append(prefix, out...), nil

	case modeCTR:
		out := make([]byte, len(plaintext))
		cipher.NewCTR(suite.block, iv).XORKeyStream(out, plaintext)
		return append(prefix, out...), nil

	case modeCFB:
		out := make([]byte, len(plaintext))
		// CFB is deprecated in crypto/cipher and is here only to read and write
		// data belonging to systems that already chose it.
		cipher.NewCFBEncrypter(suite.block, iv).XORKeyStream(out, plaintext) //nolint:staticcheck // interoperability with existing data
		return append(prefix, out...), nil
	}
	return nil, fmt.Errorf("algorithm %q has no encryption mode", suite.spec.name)
}

// openWith reverses sealWith over the decoded payload bytes.
//
// Error messages never include the value or the key, and an authenticated
// algorithm deliberately reports a wrong key and a tampered value identically,
// because it cannot distinguish them and guessing would be misleading.
func openWith(c *cipherConfig, suite *cipherSuite, payload []byte) ([]byte, error) {
	iv := c.fixedIV
	body := payload
	if c.ivPlacement == ivPrefix {
		if len(payload) < suite.spec.ivSize {
			return nil, fmt.Errorf("ciphertext is too short to contain the %d-byte iv %s expects",
				suite.spec.ivSize, suite.spec.name)
		}
		iv, body = payload[:suite.spec.ivSize], payload[suite.spec.ivSize:]
	}

	switch suite.spec.mode {
	case modeAEAD:
		out, err := suite.aead.Open(nil, iv, body, c.aad)
		if err != nil {
			return nil, errors.New("authentication failed: wrong key, wrong aad, or altered ciphertext")
		}
		return out, nil

	case modeCBC:
		if len(body) == 0 || len(body)%aes.BlockSize != 0 {
			return nil, errors.New("ciphertext is not a whole number of blocks")
		}
		out := make([]byte, len(body))
		cipher.NewCBCDecrypter(suite.block, iv).CryptBlocks(out, body)
		return pkcs7Unpad(out, aes.BlockSize)

	case modeCTR:
		out := make([]byte, len(body))
		cipher.NewCTR(suite.block, iv).XORKeyStream(out, body)
		return out, nil

	case modeCFB:
		out := make([]byte, len(body))
		cipher.NewCFBDecrypter(suite.block, iv).XORKeyStream(out, body) //nolint:staticcheck // interoperability with existing data
		return out, nil
	}
	return nil, fmt.Errorf("algorithm %q has no decryption mode", suite.spec.name)
}

// sealValue encrypts one field value into its final string form.
func sealValue(c *cipherConfig, suite *cipherSuite, plaintext string) (string, error) {
	payload, err := sealWith(c, suite, []byte(plaintext))
	if err != nil {
		return "", err
	}
	encoded := c.encoding.encode(payload)

	switch {
	case c.format == formatRaw:
		return encoded, nil
	case c.legacyShaped():
		return envelopePrefixV1 + encoded, nil
	default:
		return envelopePrefixV2 + c.spec.name + ":" + encoded, nil
	}
}

// openValue reverses sealValue for one field value.
//
// In envelope format the value carries its own framing, and an enc:v2: value
// names the algorithm it was written with. That name wins over the node's
// configured algorithm when the operator did not choose one explicitly — the
// value knows better — and is reported as a conflict when they did, so a
// misconfigured node says what is actually wrong instead of failing with a
// generic authentication error.
func openValue(c *cipherConfig, suite *cipherSuite, value string) (string, error) {
	effective := c
	effectiveSuite := suite
	body := value

	switch {
	case c.format == formatRaw:
		// No framing: the node's configuration is the only description of the
		// value, which is the point.

	case strings.HasPrefix(value, envelopePrefixV1):
		body = strings.TrimPrefix(value, envelopePrefixV1)
		// enc:v1: predates the picker and can only mean AES-256-GCM with a
		// base64 payload and a prepended nonce. The configured key derivation is
		// still honoured, since that is the only part v1 never pinned.
		if c.spec.name != defaultAlgorithm || c.encoding != encodingBase64 ||
			c.ivPlacement != ivPrefix || len(c.aad) != 0 {
			var err error
			if effective, effectiveSuite, err = c.asLegacyV1(); err != nil {
				return "", err
			}
		}

	case strings.HasPrefix(value, envelopePrefixV2):
		rest := strings.TrimPrefix(value, envelopePrefixV2)
		name, payload, ok := strings.Cut(rest, ":")
		if !ok {
			return "", errors.New("enc:v2: envelope is malformed: no algorithm segment")
		}
		body = payload

		if name != c.spec.name {
			if c.algorithmExplicit {
				return "", fmt.Errorf(
					"value was encrypted with %s but this node is configured for %s",
					name, c.spec.name)
			}
			var err error
			if effective, effectiveSuite, err = c.withAlgorithm(name); err != nil {
				return "", err
			}
		}

	default:
		// Callers check hasEnvelope before reaching here in envelope format.
		return "", errors.New("value is not encrypted")
	}

	raw, err := effective.encoding.decode(body)
	if err != nil {
		return "", err
	}
	plaintext, err := openWith(effective, effectiveSuite, raw)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

// asLegacyV1 returns a copy of the config describing exactly the enc:v1: scheme,
// so a node whose algorithm has since been changed can still read data written
// before the change.
func (c *cipherConfig) asLegacyV1() (*cipherConfig, *cipherSuite, error) {
	clone := *c
	clone.spec = algorithms[defaultAlgorithm]
	clone.encoding = encodingBase64
	clone.ivPlacement = ivPrefix
	clone.fixedIV = nil
	clone.aad = nil

	suite, err := resolveCipher(&clone)
	if err != nil {
		return nil, nil, err
	}
	return &clone, suite, nil
}

// withAlgorithm returns a copy of the config using the named algorithm, for
// following an enc:v2: envelope that names one the node did not configure.
func (c *cipherConfig) withAlgorithm(name string) (*cipherConfig, *cipherSuite, error) {
	spec, ok := algorithms[name]
	if !ok {
		return nil, nil, fmt.Errorf(
			"value was encrypted with %q, which this version of Hermod does not support", name)
	}
	clone := *c
	clone.spec = spec
	if clone.ivPlacement == ivFixed && len(clone.fixedIV) != spec.ivSize {
		return nil, nil, fmt.Errorf(
			"value was encrypted with %s, which needs a %d-byte iv, but the configured iv is %d bytes",
			name, spec.ivSize, len(clone.fixedIV))
	}

	suite, err := resolveCipher(&clone)
	if err != nil {
		return nil, nil, err
	}
	return &clone, suite, nil
}
