package security

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// Detection answers "how was this value encrypted?" by trying the plausible
// configurations against a sample and reporting which ones actually read it.
//
// It exists because the settings interact. The key format decides the key
// bytes, the encoding decides the payload bytes, the nonce length decides where
// the ciphertext starts, the tag placement decides which end the tag is on, and
// the AAD decides whether authentication can succeed at all — so one wrong
// setting looks exactly like all of them wrong, and an operator matching an
// external system has no signal to search by. Working that out by hand is what
// this replaces.
//
// The search is only honest for authenticated algorithms, where the tag makes a
// candidate a yes or a no. For CBC, CTR and CFB there is nothing to verify
// against, so a match is plausible plaintext and is labelled as such.
//
// Deliberately not searched: PBKDF2 and scrypt. Their salt and cost parameters
// are inputs, not properties of the ciphertext, and guessing a salt is not a
// search — it is a dictionary attack against the operator's own data. When
// nothing matches, the reason says so.

// Confidence describes how far a candidate can be trusted.
type Confidence string

const (
	// ConfidenceCertain means an authenticated algorithm verified the tag. The
	// configuration is not a guess: nothing else could have produced this value.
	ConfidenceCertain Confidence = "certain"
	// ConfidenceLikely means an unauthenticated algorithm produced something
	// that looks like plaintext. It can be a coincidence, and on a short value
	// it sometimes is.
	ConfidenceLikely Confidence = "likely"
)

// previewLimit bounds the plaintext echoed back.
//
// The preview is there so an operator recognises their own data, not so this
// becomes a bulk decryption service on a hot endpoint. Enough to see a JSON
// object start and a familiar field; not enough to exfiltrate a row.
const previewLimit = 64

// Candidate is one configuration that read the sample.
type Candidate struct {
	// Config is the node settings to apply, ready to merge into a decrypt node.
	Config map[string]any `json:"config"`
	// Label describes the candidate in the terms an operator would use.
	Label string `json:"label"`
	// Confidence is certain only when a tag was verified.
	Confidence Confidence `json:"confidence"`
	// Preview is the leading, truncated plaintext.
	Preview string `json:"preview"`
}

// DetectionResult is the answer, ordered best-first.
type DetectionResult struct {
	Candidates []Candidate `json:"candidates"`
	// Reason explains an empty or weak result. It never contains the key.
	Reason string `json:"reason,omitempty"`
}

func sha256Sum(b []byte) []byte {
	sum := sha256.Sum256(b)
	return sum[:]
}

// keyFormatsFor lists the key derivations worth trying for a given key size.
//
// The fixed-length formats are only tried when the supplied key actually
// decodes to that length, which is what keeps the search small: a 32-character
// key is plausibly raw AES-256 bytes and is certainly a passphrase, but it is
// only hex or base64 if it decodes cleanly.
func keyFormatsFor(key string, keySize int) []keyFormat {
	out := []keyFormat{keyPassphrase}
	if len(key) == keySize {
		out = append(out, keyRaw)
	}
	if b, err := hex.DecodeString(strings.TrimSpace(key)); err == nil && len(b) == keySize {
		out = append(out, keyHex)
	}
	if b, err := encodingBase64.decode(key); err == nil && len(b) == keySize {
		out = append(out, keyBase64)
	}
	return out
}

// detectionSpace enumerates the settings tried for one algorithm.
type detectionSpace struct {
	nonceSizes    []int
	tagPlacements []tagPlacement
	aadSources    []aadSource
}

func spaceFor(spec algorithmSpec) detectionSpace {
	if !spec.authenticated() {
		return detectionSpace{
			nonceSizes:    []int{spec.ivSize},
			tagPlacements: []tagPlacement{tagSuffix},
			aadSources:    []aadSource{aadNone},
		}
	}
	nonces := []int{spec.ivSize}
	if !spec.fixedNonce {
		// 12 is the standard, 16 is the common deviation. Anything else is rare
		// enough that searching for it costs more than it returns.
		nonces = []int{12, 16}
	}
	return detectionSpace{
		nonceSizes:    nonces,
		tagPlacements: []tagPlacement{tagSuffix, tagPrefix},
		aadSources:    []aadSource{aadNone, aadKey},
	}
}

// DetectDecryption searches for a configuration that reads sample under key.
//
// It is deliberately a pure function over the two inputs, with no access to
// stored configuration: the caller supplies the key it already holds, so this
// grants no ability that caller did not already have.
func DetectDecryption(sample, key string) DetectionResult {
	sample = strings.TrimSpace(sample)
	switch {
	case sample == "":
		return DetectionResult{Reason: "no sample value: paste an encrypted value to detect its settings"}
	case key == "":
		return DetectionResult{Reason: "no key: detection works by trying to decrypt, so it needs the key"}
	}

	if hasEnvelope(sample) {
		return detectEnvelope(sample, key)
	}
	return detectRaw(sample, key)
}

// detectEnvelope handles values this package wrote. They are self-describing,
// so there is nothing to search — only to confirm that the key is right.
func detectEnvelope(sample, key string) DetectionResult {
	alg := defaultAlgorithm
	if rest, ok := strings.CutPrefix(sample, envelopePrefixV2); ok {
		if name, _, found := strings.Cut(rest, ":"); found {
			alg = name
		}
	}
	if _, known := algorithms[alg]; !known {
		return DetectionResult{Reason: fmt.Sprintf(
			"the value names algorithm %q, which this version of Hermod does not support", alg)}
	}

	for _, kf := range keyFormatsFor(key, algorithms[alg].keySize) {
		cfg := map[string]any{
			"format": string(formatEnvelope), "algorithm": alg,
			"keyFormat": string(kf), "key": key,
		}
		if plain, ok := trial(cfg, sample); ok {
			return DetectionResult{Candidates: []Candidate{{
				Config: publicConfig(cfg),
				Label: fmt.Sprintf("Hermod envelope, %s, %s key",
					alg, kf),
				Confidence: ConfidenceCertain,
				Preview:    truncate(plain),
			}}}
		}
	}

	return DetectionResult{Reason: "this is a Hermod envelope, so the settings are already known — " +
		"but the key does not open it. Check the key and the key format."}
}

// detectRaw searches the configuration space for a value with no envelope.
//
// The search is a product of independent axes, so it is written as one loop per
// axis delegating to the next rather than as five nested ones. The order is
// deliberate: encodings first because a failed decode prunes an entire subtree,
// then algorithms, then the settings that only exist for some of them.
func detectRaw(sample, key string) DetectionResult {
	var found []Candidate

	for _, enc := range []payloadEncoding{encodingBase64, encodingBase64URL, encodingHex} {
		if _, err := enc.decode(sample); err != nil {
			continue // not this encoding; the whole subtree is unreachable
		}
		for _, algName := range SupportedAlgorithms() {
			found = append(found, candidatesForAlgorithm(sample, key, enc, algName)...)
		}
	}

	sort.SliceStable(found, func(i, j int) bool {
		return found[i].Confidence == ConfidenceCertain && found[j].Confidence != ConfidenceCertain
	})
	found = collapseEncodings(found)

	if len(found) == 0 {
		return DetectionResult{Reason: "no combination of algorithm, key format, encoding, nonce length, " +
			"tag position or AAD read this value. The key may be wrong, or derived with PBKDF2 or " +
			"scrypt — those take a salt and cost parameters that are not recoverable from the " +
			"ciphertext, so set them by hand. A fixed IV is also not detectable."}
	}
	return DetectionResult{Candidates: found}
}

// candidatesForAlgorithm walks the settings that apply to one algorithm.
func candidatesForAlgorithm(sample, key string, enc payloadEncoding, algName string) []Candidate {
	spec := algorithms[algName]
	space := spaceFor(spec)

	var out []Candidate
	for _, kf := range keyFormatsFor(key, spec.keySize) {
		for _, ns := range space.nonceSizes {
			for _, tp := range space.tagPlacements {
				for _, as := range space.aadSources {
					if c, ok := tryCandidate(sample, key, enc, spec, kf, ns, tp, as); ok {
						out = append(out, c)
					}
				}
			}
		}
	}
	return out
}

// tryCandidate builds one configuration and reports whether it read the sample.
func tryCandidate(
	sample, key string, enc payloadEncoding, spec algorithmSpec,
	kf keyFormat, ns int, tp tagPlacement, as aadSource,
) (Candidate, bool) {
	// Only settings the algorithm actually accepts go in. nonceSize is rejected
	// outright for the block modes and for the Poly1305 constructions, so
	// including it unconditionally made every one of those candidates fail to
	// parse rather than fail to decrypt.
	cfg := map[string]any{
		"format": string(formatRaw), "algorithm": spec.name,
		"encoding": string(enc), "keyFormat": string(kf),
		"key": key, "ivPlacement": string(ivPrefix),
	}
	if spec.authenticated() {
		cfg["tagPlacement"] = string(tp)
		cfg["aadMode"] = string(as)
		if !spec.fixedNonce {
			cfg["nonceSize"] = ns
		}
	}

	plain, ok := trial(cfg, sample)
	if !ok {
		return Candidate{}, false
	}

	conf := ConfidenceCertain
	if !spec.authenticated() {
		if !plausiblePlaintext([]byte(plain)) {
			return Candidate{}, false
		}
		conf = ConfidenceLikely
	}

	return Candidate{
		Config:     publicConfig(cfg),
		Label:      describeCandidate(spec.name, kf, enc, ns, tp, as),
		Confidence: conf,
		Preview:    truncate(plain),
	}, true
}

// trial runs one candidate configuration against the sample.
func trial(cfg map[string]any, sample string) (string, bool) {
	c, err := parseCipherConfig(cfg)
	if err != nil {
		return "", false
	}
	suite, err := resolveCipher(c)
	if err != nil {
		return "", false
	}
	plain, err := openValue(c, suite, sample)
	if err != nil {
		return "", false
	}
	return plain, true
}

// publicConfig strips the key and the internal sample from a candidate, so the
// settings can be handed to a UI without echoing the secret back.
func publicConfig(cfg map[string]any) map[string]any {
	out := make(map[string]any, len(cfg))
	for k, v := range cfg {
		if k == "key" || strings.HasPrefix(k, "_") {
			continue
		}
		out[k] = v
	}
	return out
}

func describeCandidate(alg string, kf keyFormat, enc payloadEncoding, ns int, tp tagPlacement, as aadSource) string {
	parts := []string{alg, string(enc) + " payload", string(kf) + " key"}
	if algorithms[alg].authenticated() {
		parts = append(parts, fmt.Sprintf("%d-byte nonce", ns))
		if tp == tagPrefix {
			parts = append(parts, "tag before ciphertext")
		}
		if as == aadKey {
			parts = append(parts, "key used as AAD")
		}
	}
	return strings.Join(parts, ", ")
}

// collapseEncodings drops candidates that differ from an earlier one only in
// the payload encoding.
//
// base64 and base64url share every character except -_ and +/, so a value using
// none of those decodes identically under both. Reporting each as a separate
// option asks the operator to pick between two settings that do the same thing.
// The enumeration tries base64 first, so the standard alphabet is the one kept.
func collapseEncodings(in []Candidate) []Candidate {
	seen := make(map[string]bool, len(in))
	out := in[:0]
	for _, c := range in {
		k := fmt.Sprintf("%v|%v|%v|%v|%v|%v|%v",
			c.Config["format"], c.Config["algorithm"], c.Config["keyFormat"],
			c.Config["ivPlacement"], c.Config["nonceSize"],
			c.Config["tagPlacement"], c.Config["aadMode"])
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, c)
	}
	return out
}

func truncate(s string) string {
	if len(s) <= previewLimit {
		return s
	}
	return s[:previewLimit] + "…"
}
