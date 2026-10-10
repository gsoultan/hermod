package evaluator

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/pkg/comm/message"
)

// rfc4231Case2 is HMAC-SHA256 test case 2 of RFC 4231: key "Jefe".
const (
	rfc4231Case2Data = "what do ya want for nothing?"
	rfc4231Case2MAC  = "5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843"
)

func hmacHex(key, data string) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(data))
	return hex.EncodeToString(mac.Sum(nil))
}

// hmac_sha256 signs with a secret it is given the name of, and never with a
// key written into the expression: a key in a workflow's config is a key in
// every export, every preview and every screen share of it. The second
// argument is always a name, so the right key typed inline signs nothing.
func TestHMACSHA256SignsWithANamedSecret(t *testing.T) {
	t.Cleanup(func() { SetSecretSource(nil) })
	SetSecretSource(fakeScopedSource{
		"tenant-a/WEBHOOK_KEY": "Jefe",
		"tenant-b/WEBHOOK_KEY": "b-key",
		"/WEBHOOK_KEY":         "global-key",
		"tenant-a/EMPTY":       "",
	})

	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetVHost("tenant-a")
	msg.SetData("body", rfc4231Case2Data)
	msg.SetData("key_name", "WEBHOOK_KEY")

	cases := []struct {
		name string
		expr string
		want any
	}{
		{"the vhost's secret", "hmac_sha256(source.body, 'WEBHOOK_KEY')", rfc4231Case2MAC},
		{"the name may come from a field", "hmac_sha256(source.body, source.key_name)", rfc4231Case2MAC},
		{"the name is case-insensitive as every function's is", "HMAC_SHA256(source.body, 'WEBHOOK_KEY')", rfc4231Case2MAC},
		{"the key written inline is a name, and no secret has it", "hmac_sha256(source.body, 'Jefe')", nil},
		{"a secret's value is not a name either", "hmac_sha256(source.body, secret('WEBHOOK_KEY'))", nil},
		{"a secret that does not exist", "hmac_sha256(source.body, 'NOPE')", nil},
		{"an empty secret signs nothing", "hmac_sha256(source.body, 'EMPTY')", nil},
		{"no secret named", "hmac_sha256(source.body)", nil},
		{"nothing to sign", "hmac_sha256(source.missing, 'WEBHOOK_KEY')", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := NewEvaluator().ParseAndEvaluate(msg, c.expr); got != c.want {
				t.Errorf("%s = %#v, want %#v", c.expr, got, c.want)
			}
		})
	}

	// Through a template, as a request header is built.
	if got := ResolveTemplateMsg(`sha256={{hmac_sha256(source.body, "WEBHOOK_KEY")}}`, msg); got != "sha256="+rfc4231Case2MAC {
		t.Errorf("template = %q, want the vhost's signature", got)
	}

	// Another vhost's message signs with its own key.
	other := message.AcquireMessage()
	t.Cleanup(other.Release)
	other.SetVHost("tenant-b")
	other.SetData("body", rfc4231Case2Data)
	if got, want := NewEvaluator().ParseAndEvaluate(other, "hmac_sha256(source.body, 'WEBHOOK_KEY')"), hmacHex("b-key", rfc4231Case2Data); got != want {
		t.Errorf("tenant-b signed with %#v, want its own key's %q", got, want)
	}

	// Called without a message there is no vhost: the global manager only.
	if got, want := NewEvaluator().CallFunction("hmac_sha256", []any{rfc4231Case2Data, "WEBHOOK_KEY"}), hmacHex("global-key", rfc4231Case2Data); got != want {
		t.Errorf("CallFunction = %#v, want the global key's %q", got, want)
	}
}

// A pattern can come from message data, so its size is bounded before it is
// compiled or cached. RE2 matches in linear time, but compiling a huge pattern
// is not free, and every distinct one would take a slot in the shared cache.
func TestRegexFunctionsRefuseAnOverlongPattern(t *testing.T) {
	subject := strings.Repeat("a", maxFunctionPatternLen+1)
	atCap := strings.Repeat("a", maxFunctionPatternLen)
	overCap := atCap + "a"

	e := NewEvaluator()
	if got := e.CallFunction("regex_extract", []any{subject, atCap}); got != atCap {
		t.Errorf("a pattern at the cap: got %#v, want the match", got)
	}
	if got := e.CallFunction("regex_extract", []any{subject, overCap}); got != nil {
		t.Errorf("a pattern over the cap: got %#v, want nil", got)
	}
	if got := e.CallFunction("regex_replace", []any{subject, atCap, ""}); got != "a" {
		t.Errorf("regex_replace at the cap: got %#v, want \"a\"", got)
	}
	if got := e.CallFunction("regex_replace", []any{subject, overCap, ""}); got != nil {
		t.Errorf("regex_replace over the cap: got %#v, want nil", got)
	}
}
