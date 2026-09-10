package security

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/transformer"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
)

func init() {
	transformer.Register("encrypt", &EncryptTransformer{})
	transformer.Register("decrypt", &DecryptTransformer{})
}

var (
	errNoKey    = errors.New("no encryption key configured: set \"key\" on the node")
	errNoFields = errors.New("no fields configured: set \"field\" or \"fields\" on the node")
)

// plaintextFor renders a field value as the text to encrypt.
//
// Composite values are refused unless serializeJSON is set. Rendering a map
// with %v yields Go syntax ("map[email:a@b.com]"), and decrypt would hand that
// literal string back in place of the object — a lossy round trip that reports
// success. JSON is the encoding that survives, so with the opt-in the subtree is
// marshalled and sealed as one document; decrypt's parseJson turns it back into
// an object. Without it, name the leaf fields instead.
func plaintextFor(val any, serializeJSON bool) (string, error) {
	switch v := val.(type) {
	case string:
		return v, nil
	case []byte:
		return string(v), nil
	}

	switch reflect.ValueOf(val).Kind() {
	case reflect.Map, reflect.Slice, reflect.Array:
		if !serializeJSON {
			return "", fmt.Errorf(
				"value is a %T; only scalar values can be encrypted. "+
					"Set \"serializeJson\" to seal it as one JSON document, or name the leaf fields",
				val)
		}
		raw, err := json.Marshal(val)
		if err != nil {
			return "", fmt.Errorf("value is a %T and cannot be encoded as JSON: %w", val, err)
		}
		return string(raw), nil
	}
	return fmt.Sprintf("%v", val), nil
}

// parseJSONMode says what decrypt does with a plaintext that contains JSON.
//
// A decrypted value is a string. When the column holds a whole JSON document
// that string is not what the rest of the pipeline wants: no downstream node can
// address into it, and the live preview renders it as one escaped line instead
// of a tree. Parsing is opt-in because it changes a field's type, and doing that
// silently would reshape every message flowing through an existing node.
type parseJSONMode string

const (
	// parseJSONOff leaves the decrypted string alone. The default.
	parseJSONOff parseJSONMode = "off"
	// parseJSONObjects parses only values that are a JSON object or array, and
	// leaves anything else — including bare numbers and booleans, which are
	// valid JSON — as the string it decrypted to. A value that looks like JSON
	// but does not parse is also left alone.
	parseJSONObjects parseJSONMode = "objects"
	// parseJSONStrict treats the whole value as a JSON document: scalars are
	// parsed too, and a value that will not parse takes the onError policy.
	parseJSONStrict parseJSONMode = "strict"
)

func configuredParseJSON(config map[string]any) (parseJSONMode, error) {
	raw, _ := config["parseJson"].(string)
	switch mode := parseJSONMode(strings.ToLower(strings.TrimSpace(raw))); mode {
	case "", parseJSONOff:
		return parseJSONOff, nil
	case parseJSONObjects, parseJSONStrict:
		return mode, nil
	default:
		return "", fmt.Errorf("unknown parseJson %q: use off, objects or strict", raw)
	}
}

// configuredSerializeJSON reads the encrypt-side opt-in, which arrives as a Go
// bool from a workflow config and as a JSON bool or a form string otherwise.
func configuredSerializeJSON(config map[string]any) bool {
	switch v := config["serializeJson"].(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "true")
	}
	return false
}

// looksLikeJSONDocument reports whether the text begins an object or array.
//
// This is what keeps "objects" mode from changing the type of a scalar: a
// decrypted "12345" parses as JSON perfectly well, and turning it into a number
// would be a schema change downstream that nobody asked for.
func looksLikeJSONDocument(s string) bool {
	trimmed := strings.TrimLeft(s, " \t\r\n")
	return strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")
}

// decodeJSONPlaintext applies the parse mode to one decrypted value.
//
// The second return value reports whether the parse produced a structured
// value; false means the caller should keep the string it already has.
func decodeJSONPlaintext(mode parseJSONMode, plaintext string) (any, bool, error) {
	switch mode {
	case parseJSONObjects:
		// Validity is checked before parsing rather than by discarding the parse
		// error, so the only error this branch can return is a real one. A value
		// that opens like a document but is not one is left as it is: "objects"
		// is the lenient mode, and "strict" is how an operator asks to be told.
		if !looksLikeJSONDocument(plaintext) || !json.Valid([]byte(plaintext)) {
			return nil, false, nil
		}
		var out any
		if err := json.Unmarshal([]byte(plaintext), &out); err != nil {
			return nil, false, fmt.Errorf("decrypted value is not valid JSON: %w", err)
		}
		return out, true, nil

	case parseJSONStrict:
		var out any
		if err := json.Unmarshal([]byte(plaintext), &out); err != nil {
			return nil, false, errors.New("decrypted value is not valid JSON")
		}
		return out, true, nil
	}
	return nil, false, nil
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

// onPlaintextPolicy governs what an *unencrypted* value does to the message,
// in envelope format only. Raw format has no envelope, so it cannot tell
// plaintext from ciphertext and never consults this.
//
// This exists because the pass-through default is the single easiest way for a
// decrypt node to appear healthy while doing nothing at all: point it at a
// column encrypted by another system, and every value fails the envelope check
// and sails through untouched with no error and nothing in the logs. Operators
// who have finished a rollout should set this to "fail" so that silence becomes
// a stopped pipeline instead of a wrong one.
type onPlaintextPolicy string

const (
	// onPlaintextPassthrough leaves the value alone. The default, because during
	// a rollout a column genuinely holds a mix of encrypted and plain values.
	onPlaintextPassthrough onPlaintextPolicy = "passthrough"
	// onPlaintextFail aborts the message.
	onPlaintextFail onPlaintextPolicy = "fail"
	// onPlaintextNull replaces the value with nil.
	onPlaintextNull onPlaintextPolicy = "null"
)

func configuredOnPlaintext(config map[string]any) onPlaintextPolicy {
	policy, _ := config["onPlaintext"].(string)
	switch onPlaintextPolicy(strings.ToLower(strings.TrimSpace(policy))) {
	case onPlaintextFail:
		return onPlaintextFail
	case onPlaintextNull:
		return onPlaintextNull
	default:
		return onPlaintextPassthrough
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

// setup resolves the field list and the cipher for one Transform call.
//
// Both are validated before any field is touched, so a misconfigured node fails
// on its first message with one clear message rather than emitting a record
// that is half transformed.
func setup(config map[string]any) ([]string, *cipherConfig, *cipherSuite, error) {
	fields := configuredFields(config)
	if len(fields) == 0 {
		return nil, nil, nil, errNoFields
	}
	cfg, err := parseCipherConfig(config)
	if err != nil {
		return nil, nil, nil, err
	}
	suite, err := resolveCipher(cfg)
	if err != nil {
		return nil, nil, nil, err
	}
	return fields, cfg, suite, nil
}

// EncryptTransformer encrypts named fields with a configurable algorithm.
//
// The default is AES-256-GCM with a fresh random nonce per value, so encrypting
// the same plaintext twice yields different ciphertexts. That is the safe
// default, and it means an encrypted column cannot be used as a join or lookup
// key downstream.
type EncryptTransformer struct{}

func (t *EncryptTransformer) Prepare(config map[string]any) (map[string]any, error) {
	return prepareFields(config)
}

func (t *EncryptTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}

	fields, cfg, suite, err := setup(config)
	if err != nil {
		return nil, fmt.Errorf("encrypt: %w", err)
	}
	serializeJSON := configuredSerializeJSON(config)

	for _, field := range fields {
		val := evaluator.GetMsgValByPath(msg, field)
		if val == nil {
			// Absent field. Encrypting a value that is not there would mean
			// inventing one, so leave the message shaped as it arrived.
			continue
		}

		plaintext, err := plaintextFor(val, serializeJSON)
		if err != nil {
			return nil, fmt.Errorf("encrypt: field %q: %w", field, err)
		}

		// Only the envelope formats can recognise their own output. In raw
		// format there is no marker, so re-running the node over a column it
		// already encrypted will encrypt it a second time; that trade-off is the
		// reason envelope is the default and is called out in the editor.
		if cfg.format == formatEnvelope && hasEnvelope(plaintext) {
			continue
		}

		sealed, err := sealValue(cfg, suite, plaintext)
		if err != nil {
			return nil, fmt.Errorf("encrypt: field %q: %w", field, err)
		}
		msg.SetData(field, sealed)
	}

	return msg, nil
}

// DecryptTransformer reverses EncryptTransformer for named fields, and in raw
// format reads ciphertext written by systems other than Hermod.
type DecryptTransformer struct{}

func (t *DecryptTransformer) Prepare(config map[string]any) (map[string]any, error) {
	return prepareFields(config)
}

func (t *DecryptTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}

	fields, cfg, suite, err := setup(config)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}

	onError := configuredOnError(config)
	onPlaintext := configuredOnPlaintext(config)

	// Validated before the walk starts: a typo in parseJson must stop the node
	// rather than quietly mean "off" and hand a string to a downstream node that
	// is addressing into an object.
	parseJSON, err := configuredParseJSON(config)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}

	for _, field := range fields {
		val := evaluator.GetMsgValByPath(msg, field)
		if val == nil {
			continue
		}

		text, ok := val.(string)
		if !ok {
			if b, isBytes := val.([]byte); isBytes {
				text, ok = string(b), true
			}
		}
		if !ok {
			// A number or a bool cannot be ciphertext under any of the supported
			// encodings, so this is a field list pointing at the wrong column
			// rather than a decryption failure.
			if err := applyPlaintextPolicy(msg, field, onPlaintext, "value is a %T, not encrypted text", val); err != nil {
				return nil, err
			}
			continue
		}

		// In envelope format an unmarked value was not written by this package.
		// During a rollout a column holds a mix of both, so the default passes it
		// through — but that default is also how a decrypt node pointed at
		// foreign ciphertext stays silent, which is why the policy is settable
		// and why raw format skips this check entirely.
		if cfg.format == formatEnvelope && !hasEnvelope(text) {
			if err := applyPlaintextPolicy(msg, field, onPlaintext, "value has no enc:v1:/enc:v2: envelope, so it was not encrypted by an encrypt node; if it was encrypted elsewhere, set format to \"raw\" and describe the scheme"); err != nil {
				return nil, err
			}
			continue
		}

		plaintext, err := openValue(cfg, suite, text)
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

		// A decrypted document becomes a value the rest of the pipeline can
		// address into, when the node asks for it. A parse failure is a
		// per-value failure like a decryption failure, so it takes the same
		// policy rather than inventing a second one.
		decoded, ok, err := decodeJSONPlaintext(parseJSON, plaintext)
		if err != nil {
			switch onError {
			case onErrorSkip:
				msg.SetData(field, plaintext)
				continue
			case onErrorNull:
				msg.SetData(field, nil)
				continue
			default:
				return nil, fmt.Errorf("decrypt: field %q: %w", field, err)
			}
		}
		if ok {
			msg.SetData(field, decoded)
			continue
		}
		msg.SetData(field, plaintext)
	}

	return msg, nil
}

// applyPlaintextPolicy handles a value that is not this package's ciphertext.
// It returns an error only when the policy is "fail"; the message is named in
// that error so an operator can see which field is not what they expected.
func applyPlaintextPolicy(msg hermod.Message, field string, policy onPlaintextPolicy, format string, args ...any) error {
	switch policy {
	case onPlaintextFail:
		return fmt.Errorf("decrypt: field %q: %s", field, fmt.Sprintf(format, args...))
	case onPlaintextNull:
		msg.SetData(field, nil)
		return nil
	default:
		return nil
	}
}
