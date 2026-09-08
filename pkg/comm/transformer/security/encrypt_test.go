package security

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/comm/transformer"
)

const testKey = "correct horse battery staple"

// newMsg builds a message carrying the supplied top-level fields.
func newMsg(t *testing.T, fields map[string]any) *message.DefaultMessage {
	t.Helper()
	msg := message.AcquireMessage()
	t.Cleanup(func() { message.ReleaseMessage(msg) })
	msg.SetID("1")
	for k, v := range fields {
		msg.SetData(k, v)
	}
	return msg
}

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	enc := &EncryptTransformer{}
	dec := &DecryptTransformer{}
	ctx := t.Context()

	msg := newMsg(t, map[string]any{"ssn": "123-45-6789", "keep": "untouched"})
	cfg := map[string]any{"field": "ssn", "key": testKey}

	res, err := enc.Transform(ctx, msg, cfg)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	got, _ := res.Data()["ssn"].(string)
	if got == "123-45-6789" {
		t.Fatal("value was not encrypted")
	}
	if !strings.HasPrefix(got, "enc:v1:") {
		t.Fatalf("ciphertext missing envelope prefix: %q", got)
	}
	if res.Data()["keep"] != "untouched" {
		t.Errorf("unconfigured field was modified: %v", res.Data()["keep"])
	}

	res, err = dec.Transform(ctx, res, cfg)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if res.Data()["ssn"] != "123-45-6789" {
		t.Errorf("round trip lost data: got %v", res.Data()["ssn"])
	}
}

func TestEncrypt_NonceIsRandomPerCall(t *testing.T) {
	enc := &EncryptTransformer{}
	ctx := t.Context()
	cfg := map[string]any{"field": "v", "key": testKey}

	first := newMsg(t, map[string]any{"v": "same"})
	second := newMsg(t, map[string]any{"v": "same"})

	a, err := enc.Transform(ctx, first, cfg)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	b, err := enc.Transform(ctx, second, cfg)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	if a.Data()["v"] == b.Data()["v"] {
		t.Error("identical plaintexts produced identical ciphertexts; nonce is not random")
	}
}

func TestEncrypt_IsIdempotent(t *testing.T) {
	enc := &EncryptTransformer{}
	ctx := t.Context()
	cfg := map[string]any{"field": "v", "key": testKey}

	msg := newMsg(t, map[string]any{"v": "secret"})
	once, err := enc.Transform(ctx, msg, cfg)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	sealed, _ := once.Data()["v"].(string)

	twice, err := enc.Transform(ctx, once, cfg)
	if err != nil {
		t.Fatalf("re-encrypt: %v", err)
	}
	if twice.Data()["v"] != sealed {
		t.Error("re-running encrypt double-encrypted an already-encrypted value")
	}
}

func TestDecrypt_LeavesPlaintextUntouched(t *testing.T) {
	dec := &DecryptTransformer{}
	ctx := t.Context()

	msg := newMsg(t, map[string]any{"v": "never encrypted"})
	res, err := dec.Transform(ctx, msg, map[string]any{"field": "v", "key": testKey})
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if res.Data()["v"] != "never encrypted" {
		t.Errorf("plaintext was altered: %v", res.Data()["v"])
	}
}

func TestDecrypt_WrongKey(t *testing.T) {
	enc := &EncryptTransformer{}
	dec := &DecryptTransformer{}
	ctx := t.Context()

	msg := newMsg(t, map[string]any{"v": "secret"})
	sealed, err := enc.Transform(ctx, msg, map[string]any{"field": "v", "key": testKey})
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	ciphertext, _ := sealed.Data()["v"].(string)

	t.Run("fails closed by default", func(t *testing.T) {
		if _, err := dec.Transform(ctx, sealed, map[string]any{
			"field": "v", "key": "a different key",
		}); err == nil {
			t.Fatal("expected an error decrypting with the wrong key")
		}
	})

	t.Run("onError skip keeps ciphertext", func(t *testing.T) {
		res, err := dec.Transform(ctx, sealed, map[string]any{
			"field": "v", "key": "a different key", "onError": "skip",
		})
		if err != nil {
			t.Fatalf("onError=skip should not error: %v", err)
		}
		if res.Data()["v"] != ciphertext {
			t.Errorf("onError=skip altered the value: %v", res.Data()["v"])
		}
	})

	t.Run("onError null clears the field", func(t *testing.T) {
		res, err := dec.Transform(ctx, sealed, map[string]any{
			"field": "v", "key": "a different key", "onError": "null",
		})
		if err != nil {
			t.Fatalf("onError=null should not error: %v", err)
		}
		if v, ok := res.Data()["v"]; !ok || v != nil {
			t.Errorf("onError=null should leave a nil field, got %v", v)
		}
	})
}

func TestEncrypt_FailsClosed(t *testing.T) {
	enc := &EncryptTransformer{}
	dec := &DecryptTransformer{}
	ctx := t.Context()

	tests := []struct {
		name   string
		config map[string]any
	}{
		{"no key", map[string]any{"field": "v"}},
		{"empty key", map[string]any{"field": "v", "key": ""}},
		{"no fields", map[string]any{"key": testKey}},
		{"empty field", map[string]any{"key": testKey, "field": ""}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := enc.Transform(ctx, newMsg(t, map[string]any{"v": "x"}), tt.config); err == nil {
				t.Error("encrypt: expected an error, got nil")
			}
			if _, err := dec.Transform(ctx, newMsg(t, map[string]any{"v": "x"}), tt.config); err == nil {
				t.Error("decrypt: expected an error, got nil")
			}
		})
	}
}

func TestEncrypt_MultipleAndNestedFields(t *testing.T) {
	enc := &EncryptTransformer{}
	dec := &DecryptTransformer{}
	ctx := t.Context()

	configs := map[string]map[string]any{
		"slice of any":    {"fields": []any{"ssn", "user.email"}, "key": testKey},
		"slice of string": {"fields": []string{"ssn", "user.email"}, "key": testKey},
		"comma separated": {"fields": "ssn, user.email", "key": testKey},
	}

	for name, cfg := range configs {
		t.Run(name, func(t *testing.T) {
			msg := newMsg(t, map[string]any{
				"ssn":  "123-45-6789",
				"user": map[string]any{"email": "a@b.com", "id": 7},
			})

			res, err := enc.Transform(ctx, msg, cfg)
			if err != nil {
				t.Fatalf("encrypt: %v", err)
			}

			user, _ := res.Data()["user"].(map[string]any)
			if user == nil {
				t.Fatal("nested map disappeared")
			}
			if email, _ := user["email"].(string); !strings.HasPrefix(email, "enc:v1:") {
				t.Errorf("nested field not encrypted: %v", user["email"])
			}
			if ssn, _ := res.Data()["ssn"].(string); !strings.HasPrefix(ssn, "enc:v1:") {
				t.Errorf("top-level field not encrypted: %v", res.Data()["ssn"])
			}

			res, err = dec.Transform(ctx, res, cfg)
			if err != nil {
				t.Fatalf("decrypt: %v", err)
			}
			if res.Data()["ssn"] != "123-45-6789" {
				t.Errorf("ssn round trip failed: %v", res.Data()["ssn"])
			}
			user, _ = res.Data()["user"].(map[string]any)
			if user["email"] != "a@b.com" {
				t.Errorf("nested round trip failed: %v", user["email"])
			}
		})
	}
}

func TestEncrypt_MissingFieldIsSkipped(t *testing.T) {
	enc := &EncryptTransformer{}
	ctx := t.Context()

	msg := newMsg(t, map[string]any{"present": "x"})
	res, err := enc.Transform(ctx, msg, map[string]any{
		"fields": []any{"present", "absent"}, "key": testKey,
	})
	if err != nil {
		t.Fatalf("a missing field should not fail the message: %v", err)
	}
	if _, exists := res.Data()["absent"]; exists {
		t.Error("encrypt invented a field that was not in the message")
	}
}

func TestEncrypt_NonStringValue(t *testing.T) {
	enc := &EncryptTransformer{}
	dec := &DecryptTransformer{}
	ctx := t.Context()
	cfg := map[string]any{"field": "amount", "key": testKey}

	msg := newMsg(t, map[string]any{"amount": 4242})
	res, err := enc.Transform(ctx, msg, cfg)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if s, _ := res.Data()["amount"].(string); !strings.HasPrefix(s, "enc:v1:") {
		t.Fatalf("numeric field not encrypted: %v", res.Data()["amount"])
	}

	res, err = dec.Transform(ctx, res, cfg)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if res.Data()["amount"] != "4242" {
		t.Errorf("numeric round trip: got %v, want the string \"4242\"", res.Data()["amount"])
	}
}

func TestEncrypt_NilMessage(t *testing.T) {
	ctx := t.Context()
	cfg := map[string]any{"field": "v", "key": testKey}

	if res, err := (&EncryptTransformer{}).Transform(ctx, nil, cfg); err != nil || res != nil {
		t.Errorf("encrypt(nil) = %v, %v; want nil, nil", res, err)
	}
	if res, err := (&DecryptTransformer{}).Transform(ctx, nil, cfg); err != nil || res != nil {
		t.Errorf("decrypt(nil) = %v, %v; want nil, nil", res, err)
	}
}

func TestEncrypt_PrepareDoesNotLeakKey(t *testing.T) {
	enc := &EncryptTransformer{}
	prepared, err := enc.Prepare(map[string]any{"field": "v", "key": testKey})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	for k, v := range prepared {
		if k == "key" {
			continue // the caller's own value, left as it was
		}
		if s, ok := v.(string); ok && strings.Contains(s, testKey) {
			t.Errorf("prepared config copied key material into %q", k)
		}
	}
}

func TestEncrypt_Registered(t *testing.T) {
	for _, name := range []string{"encrypt", "decrypt"} {
		if _, ok := transformer.Get(name); !ok {
			t.Errorf("transformer %q is not registered", name)
		}
	}
}

func TestEncrypt_ConcurrentUse(t *testing.T) {
	enc := &EncryptTransformer{}
	dec := &DecryptTransformer{}
	ctx := t.Context()
	cfg := map[string]any{"field": "v", "key": testKey}

	var wg sync.WaitGroup
	for i := range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			msg := message.AcquireMessage()
			defer message.ReleaseMessage(msg)
			msg.SetID("c")
			msg.SetData("v", "secret")

			var out hermod.Message
			var err error
			if out, err = enc.Transform(ctx, msg, cfg); err != nil {
				t.Errorf("goroutine %d encrypt: %v", i, err)
				return
			}
			if out, err = dec.Transform(ctx, out, cfg); err != nil {
				t.Errorf("goroutine %d decrypt: %v", i, err)
				return
			}
			if out.Data()["v"] != "secret" {
				t.Errorf("goroutine %d round trip: %v", i, out.Data()["v"])
			}
		}()
	}
	wg.Wait()
}

// TestEncrypt_PreparedConfigPath exercises the flow the engine actually uses:
// Prepare once when the workflow is loaded, then Transform per message with the
// config Prepare returned (see registry_workflow.go).
func TestEncrypt_PreparedConfigPath(t *testing.T) {
	enc := &EncryptTransformer{}
	dec := &DecryptTransformer{}
	ctx := t.Context()

	// The shape a node config has after the workflow JSON is decoded.
	var encCfg map[string]any
	if err := json.Unmarshal([]byte(`{
		"transType": "encrypt",
		"fields": ["ssn", "user.email"],
		"key": "correct horse battery staple"
	}`), &encCfg); err != nil {
		t.Fatalf("decode config: %v", err)
	}

	encPrepared, err := enc.Prepare(encCfg)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if got, ok := encPrepared["_parsed_fields"].([]string); !ok || len(got) != 2 {
		t.Fatalf("prepare did not parse fields: %#v", encPrepared["_parsed_fields"])
	}

	msg := newMsg(t, map[string]any{
		"ssn":  "123-45-6789",
		"user": map[string]any{"email": "a@b.com"},
	})

	res, err := enc.Transform(ctx, msg, encPrepared)
	if err != nil {
		t.Fatalf("encrypt with prepared config: %v", err)
	}
	if s, _ := res.Data()["ssn"].(string); !strings.HasPrefix(s, "enc:v1:") {
		t.Fatalf("prepared config did not encrypt: %v", res.Data()["ssn"])
	}

	decPrepared, err := dec.Prepare(map[string]any{
		"fields": []any{"ssn", "user.email"},
		"key":    testKey,
	})
	if err != nil {
		t.Fatalf("prepare decrypt: %v", err)
	}
	res, err = dec.Transform(ctx, res, decPrepared)
	if err != nil {
		t.Fatalf("decrypt with prepared config: %v", err)
	}
	if res.Data()["ssn"] != "123-45-6789" {
		t.Errorf("prepared round trip failed: %v", res.Data()["ssn"])
	}
	user, _ := res.Data()["user"].(map[string]any)
	if user["email"] != "a@b.com" {
		t.Errorf("prepared nested round trip failed: %v", user["email"])
	}
}

// TestEncrypt_PreparedEmptyConfigStillFailsClosed guards the case Prepare can
// mask: it stores an empty parsed list, and configuredFields prefers that list
// over re-reading the config. A node with no fields must still be an error.
func TestEncrypt_PreparedEmptyConfigStillFailsClosed(t *testing.T) {
	enc := &EncryptTransformer{}
	ctx := t.Context()

	prepared, err := enc.Prepare(map[string]any{"key": testKey})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if _, err := enc.Transform(ctx, newMsg(t, map[string]any{"v": "x"}), prepared); err == nil {
		t.Error("expected an error for a prepared config with no fields")
	}
}

// TestEncrypt_RejectsNonScalar guards a silent-corruption path. Stringifying a
// map with %v yields Go syntax ("map[email:a@b.com]"), which decrypt would hand
// back as that literal string rather than the object — a lossy round trip that
// reports success. Encrypting a composite value is refused instead.
func TestEncrypt_RejectsNonScalar(t *testing.T) {
	enc := &EncryptTransformer{}
	ctx := t.Context()

	tests := []struct {
		name  string
		value any
	}{
		{"object", map[string]any{"email": "a@b.com"}},
		{"array", []any{"a", "b"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := newMsg(t, map[string]any{"v": tt.value})
			if _, err := enc.Transform(ctx, msg, map[string]any{"field": "v", "key": testKey}); err == nil {
				t.Fatalf("expected an error encrypting a %s, got nil", tt.name)
			}
		})
	}
}

// TestEncrypt_ByteSliceRoundTrips pins how binary fields behave. Field lookup
// marshals the message through JSON, so a []byte is already the base64 string
// "aGVsbG8=" by the time it is read — which is what every other node reading
// that field sees too. Encrypting preserves that representation rather than
// inventing a different one.
func TestEncrypt_ByteSliceRoundTrips(t *testing.T) {
	enc := &EncryptTransformer{}
	dec := &DecryptTransformer{}
	ctx := t.Context()
	cfg := map[string]any{"field": "blob", "key": testKey}

	msg := newMsg(t, map[string]any{"blob": []byte("hello")})
	res, err := enc.Transform(ctx, msg, cfg)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if s, _ := res.Data()["blob"].(string); !strings.HasPrefix(s, "enc:v1:") {
		t.Fatalf("byte slice not encrypted: %v", res.Data()["blob"])
	}

	res, err = dec.Transform(ctx, res, cfg)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if res.Data()["blob"] != "aGVsbG8=" {
		t.Errorf("byte slice round trip: got %v, want the base64 form \"aGVsbG8=\"", res.Data()["blob"])
	}
}
