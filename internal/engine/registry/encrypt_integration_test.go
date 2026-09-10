package registry

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/comm/transformer"
	_ "github.com/gsoultan/hermod/pkg/comm/transformer/security"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
)

// TestEngineDispatch_EncryptDecrypt runs the encrypt and decrypt transformations
// through the engine's own dispatch rather than by calling them directly.
//
// The transformers register themselves from an init() in a package the engine
// only imports for its side effect, so their unit tests can pass while a node
// configured in a real workflow resolves to nothing. This covers that seam.
func TestEngineDispatch_EncryptDecrypt(t *testing.T) {
	r := &Registry{}
	ctx := t.Context()

	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	msg.SetID("1")
	msg.SetData("ssn", "123-45-6789")

	cfg := map[string]any{"field": "ssn", "key": "engine test key"}

	out, err := r.applyTransformation(ctx, msg, "encrypt", cfg)
	if err != nil {
		t.Fatalf("engine encrypt: %v", err)
	}
	sealed, _ := out.Data()["ssn"].(string)
	if !strings.HasPrefix(sealed, "enc:v1:") {
		t.Fatalf("engine did not encrypt the field: %q", sealed)
	}

	out, err = r.applyTransformation(ctx, out, "decrypt", cfg)
	if err != nil {
		t.Fatalf("engine decrypt: %v", err)
	}
	if out.Data()["ssn"] != "123-45-6789" {
		t.Fatalf("engine round trip: got %v, want the original value", out.Data()["ssn"])
	}
}

// TestEngineDispatch_DecryptWrongKeyStopsMessage checks that a decryption
// failure surfaces as an error at the engine boundary, so the workflow's error
// path handles it instead of forwarding ciphertext to the sink.
func TestEngineDispatch_DecryptWrongKeyStopsMessage(t *testing.T) {
	r := &Registry{}
	ctx := t.Context()

	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	msg.SetID("1")
	msg.SetData("ssn", "123-45-6789")

	sealed, err := r.applyTransformation(ctx, msg, "encrypt", map[string]any{
		"field": "ssn", "key": "the right key",
	})
	if err != nil {
		t.Fatalf("engine encrypt: %v", err)
	}

	if _, err := r.applyTransformation(ctx, sealed, "decrypt", map[string]any{
		"field": "ssn", "key": "the wrong key",
	}); err == nil {
		t.Fatal("expected the engine to surface a decryption failure")
	}
}

// storageRoundTrip puts a node config through the JSON encode/decode that
// persisting and reloading a workflow performs.
//
// This is where a transformer that passes its unit tests can still fail in a
// real workflow: after the trip, every number is a float64 and every array is
// a []any, so config readers that type-assert to int or []string see nothing
// and fall back to a default the operator never chose.
func storageRoundTrip(t *testing.T, cfg map[string]any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	return out
}

// TestEngineDispatch_ConfigSurvivesStorageRoundTrip exercises the full picker
// through the engine, with every value in the shape storage hands back.
func TestEngineDispatch_ConfigSurvivesStorageRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		cfg  map[string]any
	}{
		{"gcm defaults", map[string]any{
			"fields": []string{"ssn"}, "key": "engine test key",
		}},
		{"chacha with aad", map[string]any{
			"fields": []string{"ssn"}, "key": "engine test key",
			"algorithm": "chacha20-poly1305", "aad": "tenant-42",
		}},
		{"cbc raw hex", map[string]any{
			"fields": []string{"ssn"}, "key": "0123456789abcdef0123456789abcdef",
			"keyFormat": "raw", "algorithm": "aes-256-cbc",
			"format": "raw", "encoding": "hex",
		}},
		{"pbkdf2 numeric params", map[string]any{
			"fields": []string{"ssn"}, "key": "a passphrase",
			"keyFormat": "pbkdf2", "kdfSalt": "some-salt",
			"kdfIterations": 1000, "kdfHash": "sha512",
		}},
		{"scrypt numeric params", map[string]any{
			"fields": []string{"ssn"}, "key": "a passphrase",
			"keyFormat": "scrypt", "kdfSalt": "some-salt",
			"scryptN": 1024, "scryptR": 8, "scryptP": 1,
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &Registry{}
			ctx := t.Context()

			cfg := storageRoundTrip(t, tc.cfg)

			msg := message.AcquireMessage()
			defer message.ReleaseMessage(msg)
			msg.SetID("1")
			msg.SetData("ssn", "123-45-6789")

			sealed, err := r.applyTransformation(ctx, msg, "encrypt", cfg)
			if err != nil {
				t.Fatalf("engine encrypt: %v", err)
			}
			if got, _ := sealed.Data()["ssn"].(string); got == "123-45-6789" {
				t.Fatal("engine did not encrypt the field")
			}

			// A fresh map, as a separate decrypt node would have.
			out, err := r.applyTransformation(ctx, sealed, "decrypt", storageRoundTrip(t, tc.cfg))
			if err != nil {
				t.Fatalf("engine decrypt: %v", err)
			}
			if out.Data()["ssn"] != "123-45-6789" {
				t.Fatalf("engine round trip: got %v, want the original value", out.Data()["ssn"])
			}
		})
	}
}

// TestEngineDispatch_PrepareThenTransform mirrors what a running workflow does:
// prepareWorkflowNodes calls Prepare once at start-up and the prepared map is
// what every message is then transformed with.
func TestEngineDispatch_PrepareThenTransform(t *testing.T) {
	r := &Registry{}
	ctx := t.Context()

	cfg := storageRoundTrip(t, map[string]any{
		"fields": []string{"ssn", "user.email"}, "key": "engine test key",
		"algorithm": "xchacha20-poly1305",
	})

	enc, ok := transformer.Get("encrypt")
	if !ok {
		t.Fatal("encrypt is not registered")
	}
	pt, ok := enc.(transformer.PreparedTransformer)
	if !ok {
		t.Fatal("encrypt does not implement PreparedTransformer")
	}
	prepared, err := pt.Prepare(cfg)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}

	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	msg.SetID("1")
	msg.SetData("ssn", "123-45-6789")
	msg.SetData("user.email", "a@b.com")

	sealed, err := r.applyTransformation(ctx, msg, "encrypt", prepared)
	if err != nil {
		t.Fatalf("engine encrypt: %v", err)
	}
	if got, _ := sealed.Data()["ssn"].(string); !strings.HasPrefix(got, "enc:v2:xchacha20-poly1305:") {
		t.Fatalf("expected a v2 envelope naming the algorithm, got %q", got)
	}

	out, err := r.applyTransformation(ctx, sealed, "decrypt", prepared)
	if err != nil {
		t.Fatalf("engine decrypt: %v", err)
	}
	if out.Data()["ssn"] != "123-45-6789" {
		t.Fatalf("ssn round trip: got %v", out.Data()["ssn"])
	}
	if got := out.Data()["user"]; got != nil {
		if m, isMap := got.(map[string]any); isMap && m["email"] != "a@b.com" {
			t.Fatalf("nested field round trip: got %v", m["email"])
		}
	}
}

// TestEngineDispatch_DecryptedJSONIsAddressableDownstream is the "shows up in
// another node" half of parsing.
//
// Turning the decrypted document into an object is only worth doing if the next
// node can reach into it. This chains decrypt -> mask through the engine and
// masks a field that exists only inside the parsed subtree; if decrypt had left
// a string behind, the path would resolve to nothing and mask would quietly do
// nothing at all.
func TestEngineDispatch_DecryptedJSONIsAddressableDownstream(t *testing.T) {
	r := &Registry{}
	ctx := t.Context()

	const doc = `{"name":"Ada","contact":{"email":"ada@example.com"}}`

	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	msg.SetID("1")
	msg.SetData("payload", doc)

	sealed, err := r.applyTransformation(ctx, msg, "encrypt", map[string]any{
		"fields": []string{"payload"}, "key": "chain key",
	})
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	decrypted, err := r.applyTransformation(ctx, sealed, "decrypt", map[string]any{
		"fields": []string{"payload"}, "key": "chain key", "parseJson": "objects",
	})
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}

	// The next node addresses a path that only exists if the value was parsed.
	if got := evaluator.GetMsgValByPath(decrypted, "payload.contact.email"); got != "ada@example.com" {
		t.Fatalf("a downstream node cannot address into the decrypted document: got %v", got)
	}

	masked, err := r.applyTransformation(ctx, decrypted, "mask", map[string]any{
		"field": "payload.contact.email", "maskType": "email",
	})
	if err != nil {
		t.Fatalf("downstream mask: %v", err)
	}

	got := evaluator.GetMsgValByPath(masked, "payload.contact.email")
	gotStr, _ := got.(string)
	if gotStr == "" || gotStr == "ada@example.com" {
		t.Fatalf("downstream mask did not act on the nested field: got %v", got)
	}
	t.Logf("chained decrypt -> mask produced payload.contact.email = %v", got)
}
