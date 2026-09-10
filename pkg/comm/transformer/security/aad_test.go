package security

import (
	"strings"
	"testing"
)

// AAD is the setting that is hardest to diagnose when it is wrong, because GCM
// reports a wrong key and a wrong AAD identically. These tests cover making the
// choice explicit — "none" is a selection, not an empty box — and the
// diagnostic that tells the two apart.

const aadPlain = `{"email":"someone@example.com","id":"42"}`

// sealWithAAD produces ciphertext under a given AAD configuration.
func sealWithAAD(t *testing.T, extra map[string]any) string {
	t.Helper()

	cfg := map[string]any{
		"field": "payload", "key": "0123456789abcdef0123456789abcdef",
		"keyFormat": "raw", "algorithm": "aes-256-gcm",
		"format": "raw", "encoding": "base64",
	}
	for k, v := range extra {
		cfg[k] = v
	}
	res, err := (&EncryptTransformer{}).Transform(t.Context(),
		newMsg(t, map[string]any{"payload": aadPlain}), cfg)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	s, _ := res.Data()["payload"].(string)
	return s
}

func openWithAAD(t *testing.T, ct string, extra map[string]any) (any, error) {
	t.Helper()

	cfg := map[string]any{
		"field": "payload", "key": "0123456789abcdef0123456789abcdef",
		"keyFormat": "raw", "algorithm": "aes-256-gcm",
		"format": "raw", "encoding": "base64",
	}
	for k, v := range extra {
		cfg[k] = v
	}
	res, err := (&DecryptTransformer{}).Transform(t.Context(),
		newMsg(t, map[string]any{"payload": ct}), cfg)
	if err != nil {
		return nil, err
	}
	return res.Data()["payload"], nil
}

// TestAADModeNoneIsExplicit: "no AAD" must be a choice an operator can make and
// see, not the absence of one. It is also the default.
func TestAADModeNoneIsExplicit(t *testing.T) {
	ct := sealWithAAD(t, map[string]any{"aadMode": "none"})

	for _, dec := range []map[string]any{
		{"aadMode": "none"},
		{}, // unset must mean the same thing
	} {
		got, err := openWithAAD(t, ct, dec)
		if err != nil {
			t.Fatalf("decrypt with %v: %v", dec, err)
		}
		if got != aadPlain {
			t.Fatalf("round trip: got %v", got)
		}
	}
}

// TestAADModeNoneIgnoresAStaleValue: switching the control to "none" must
// actually drop the AAD, even if a value is still sitting in the text field.
// Otherwise turning it off in the editor would silently do nothing.
func TestAADModeNoneIgnoresAStaleValue(t *testing.T) {
	ct := sealWithAAD(t, map[string]any{"aadMode": "none", "aad": "left over"})

	got, err := openWithAAD(t, ct, map[string]any{"aadMode": "none", "aad": "left over"})
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if got != aadPlain {
		t.Fatalf("got %v", got)
	}

	// And it really was sealed without AAD.
	if _, err := openWithAAD(t, ct, map[string]any{"aadMode": "value", "aad": "left over"}); err == nil {
		t.Fatal("aadMode=none still applied the stale value")
	}
}

// TestAADModeValue is the ordinary case: a fixed string both sides agree on.
func TestAADModeValue(t *testing.T) {
	ct := sealWithAAD(t, map[string]any{"aadMode": "value", "aad": "tenant-42"})

	got, err := openWithAAD(t, ct, map[string]any{"aadMode": "value", "aad": "tenant-42"})
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if got != aadPlain {
		t.Fatalf("got %v", got)
	}

	if _, err := openWithAAD(t, ct, map[string]any{"aadMode": "value", "aad": "tenant-99"}); err == nil {
		t.Fatal("a different AAD decrypted the value")
	}
}

// TestAADModeKey covers systems that pass the encryption key itself as AAD.
//
// It buys nothing cryptographically — the key is already bound by construction —
// but real systems do it, and without a preset an operator has to work out that
// the key is doubling as the AAD before anything decrypts at all.
func TestAADModeKey(t *testing.T) {
	ct := sealWithAAD(t, map[string]any{"aadMode": "key"})

	got, err := openWithAAD(t, ct, map[string]any{"aadMode": "key"})
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if got != aadPlain {
		t.Fatalf("got %v", got)
	}

	// Equivalent to spelling the key out by hand.
	got, err = openWithAAD(t, ct, map[string]any{
		"aadMode": "value", "aad": "0123456789abcdef0123456789abcdef",
	})
	if err != nil {
		t.Fatalf("decrypt with the key spelled out: %v", err)
	}
	if got != aadPlain {
		t.Fatalf("got %v", got)
	}
}

// TestLegacyAADWithoutMode: configs written before the mode control existed set
// only "aad", and must keep working.
func TestLegacyAADWithoutMode(t *testing.T) {
	ct := sealWithAAD(t, map[string]any{"aad": "tenant-42"})

	got, err := openWithAAD(t, ct, map[string]any{"aad": "tenant-42"})
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if got != aadPlain {
		t.Fatalf("got %v", got)
	}
}

// TestUnknownAADModeFails: a typo must not quietly mean "none" and produce
// ciphertext nothing can open.
func TestUnknownAADModeFails(t *testing.T) {
	_, err := openWithAAD(t, "irrelevant", map[string]any{"aadMode": "yes"})
	if err == nil {
		t.Fatal("an unknown aadMode was accepted")
	}
	if !strings.Contains(err.Error(), "yes") {
		t.Errorf("error should name the bad value, got: %v", err)
	}
}

// ---------------------------------------------------------------- diagnostics

// TestDiagnoseIdentifiesAWrongAAD is the whole point of the mode.
//
// A missing AAD and a wrong key produce the same "authentication failed" from
// GCM, which is correct cryptographically and useless operationally. With
// diagnose on, a correct key must be reported as such so the operator looks at
// the AAD instead of rotating keys.
func TestDiagnoseIdentifiesAWrongAAD(t *testing.T) {
	ct := sealWithAAD(t, map[string]any{"aadMode": "value", "aad": "tenant-42"})

	_, err := openWithAAD(t, ct, map[string]any{"aadMode": "none", "diagnose": true})
	if err == nil {
		t.Fatal("expected a failure")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "aad") {
		t.Errorf("diagnosis should point at the aad, got: %v", err)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "key") ||
		!strings.Contains(strings.ToLower(err.Error()), "correct") {
		t.Errorf("diagnosis should say the key is correct, got: %v", err)
	}
}

// TestDiagnoseIdentifiesAWrongKey is the other branch: when the trial decryption
// produces nothing sensible, the key or the framing is wrong and the AAD is not
// the thing to go looking at.
func TestDiagnoseIdentifiesAWrongKey(t *testing.T) {
	ct := sealWithAAD(t, nil)

	res, err := (&DecryptTransformer{}).Transform(t.Context(),
		newMsg(t, map[string]any{"payload": ct}),
		map[string]any{
			"field": "payload", "key": "ffffffffffffffffffffffffffffffff",
			"keyFormat": "raw", "algorithm": "aes-256-gcm",
			"format": "raw", "encoding": "base64", "diagnose": true,
		})
	if err == nil {
		t.Fatalf("expected a failure, got %v", res.Data()["payload"])
	}
	low := strings.ToLower(err.Error())
	if !strings.Contains(low, "key") {
		t.Errorf("diagnosis should point at the key, got: %v", err)
	}
	if strings.Contains(low, "key is correct") {
		t.Errorf("diagnosis wrongly reported the key as correct: %v", err)
	}
}

// TestDiagnoseNeverLeaksPlaintext: the diagnosis is produced by decrypting
// without checking the tag, so it must report a classification and nothing else.
// Putting unauthenticated plaintext in an error message would hand back exactly
// the data the authentication check exists to withhold.
func TestDiagnoseNeverLeaksPlaintext(t *testing.T) {
	ct := sealWithAAD(t, map[string]any{"aadMode": "value", "aad": "tenant-42"})

	_, err := openWithAAD(t, ct, map[string]any{"aadMode": "none", "diagnose": true})
	if err == nil {
		t.Fatal("expected a failure")
	}
	for _, secret := range []string{"someone@example.com", "42", aadPlain} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("diagnosis leaked plaintext (%q) in: %v", secret, err)
		}
	}
}

// TestDiagnoseOffStaysQuiet: the default must not run a trial decryption, and
// the message must stay the deliberately uninformative one.
func TestDiagnoseOffStaysQuiet(t *testing.T) {
	ct := sealWithAAD(t, map[string]any{"aadMode": "value", "aad": "tenant-42"})

	_, err := openWithAAD(t, ct, map[string]any{"aadMode": "none"})
	if err == nil {
		t.Fatal("expected a failure")
	}
	if strings.Contains(strings.ToLower(err.Error()), "key is correct") {
		t.Errorf("diagnosis ran without being asked for: %v", err)
	}
	// It should still tell an operator that the setting exists.
	if !strings.Contains(strings.ToLower(err.Error()), "diagnose") {
		t.Errorf("the generic failure should mention the diagnose option, got: %v", err)
	}
}

// TestDiagnoseAcrossAEADs: the trial decryption is algorithm-specific, so it has
// to be right for each authenticated construction, not just AES-GCM.
func TestDiagnoseAcrossAEADs(t *testing.T) {
	for _, alg := range []string{"aes-256-gcm", "aes-128-gcm", "chacha20-poly1305", "xchacha20-poly1305"} {
		t.Run(alg, func(t *testing.T) {
			key := "0123456789abcdef0123456789abcdef"
			if alg == "aes-128-gcm" {
				key = "0123456789abcdef"
			}
			base := map[string]any{
				"field": "payload", "key": key, "keyFormat": "raw",
				"algorithm": alg, "format": "raw", "encoding": "base64",
			}
			enc := map[string]any{"aadMode": "value", "aad": "tenant-42"}
			for k, v := range base {
				enc[k] = v
			}
			sealedMsg, err := (&EncryptTransformer{}).Transform(t.Context(),
				newMsg(t, map[string]any{"payload": aadPlain}), enc)
			if err != nil {
				t.Fatalf("encrypt: %v", err)
			}
			ct, _ := sealedMsg.Data()["payload"].(string)

			dec := map[string]any{"aadMode": "none", "diagnose": true}
			for k, v := range base {
				dec[k] = v
			}
			_, err = (&DecryptTransformer{}).Transform(t.Context(),
				newMsg(t, map[string]any{"payload": ct}), dec)
			if err == nil {
				t.Fatal("expected a failure")
			}
			if !strings.Contains(strings.ToLower(err.Error()), "aad") {
				t.Errorf("%s: diagnosis should point at the aad, got: %v", alg, err)
			}
		})
	}
}

// TestAADRejectedForUnauthenticatedAlgorithms keeps the existing guard: CBC and
// friends have nothing to authenticate AAD with, so accepting it would imply a
// binding that does not exist.
func TestAADRejectedForUnauthenticatedAlgorithms(t *testing.T) {
	_, err := (&EncryptTransformer{}).Transform(t.Context(),
		newMsg(t, map[string]any{"payload": aadPlain}),
		map[string]any{
			"field": "payload", "key": "0123456789abcdef0123456789abcdef",
			"keyFormat": "raw", "algorithm": "aes-256-cbc",
			"aadMode": "value", "aad": "tenant-42",
		})
	if err == nil {
		t.Fatal("aad was accepted for an unauthenticated algorithm")
	}
}
