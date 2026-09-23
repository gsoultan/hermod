package evaluator

import (
	"encoding/json"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

const jsonTextPayload = `{"registrationId":"reg-1","nested":{"deep":"yes"},"n":7}`

func msgWithPayload(t *testing.T, v any) hermod.Message {
	t.Helper()
	m := message.AcquireMessage()
	t.Cleanup(func() { message.ReleaseMessage(m) })
	m.SetOperation(hermod.Operation("insert"))
	m.SetData("payload", v)
	return m
}

// A column holding JSON *text* rather than a decoded object is the one shape
// that still made a SQL template disagree with the editor. The editor's sample
// always arrives JSON-decoded, so `payload` is an object there and
// `{{.payload.registrationId}}` resolves; a source that hands the same column
// over as text (a text/varchar column, MariaDB's JSON alias for LONGTEXT, a
// body that arrived as a string) left the running pipeline with nothing to
// walk. The UI's own getValByPath already parses a string mid-path, so the Go
// side was also the odd one out against its TypeScript twin.
func TestMessageResolverDescendsIntoJSONText(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload any
	}{
		{"string", jsonTextPayload},
		{"[]byte", []byte(jsonTextPayload)},
		{"json.RawMessage", json.RawMessage(jsonTextPayload)},
		{"decoded object", map[string]any{
			"registrationId": "reg-1",
			"nested":         map[string]any{"deep": "yes"},
			"n":              7,
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resolve := MessageResolver(msgWithPayload(t, tc.payload))

			if got := resolve("payload.registrationId"); got != "reg-1" {
				t.Errorf("payload.registrationId = %#v, want reg-1", got)
			}
			if got := resolve("after.payload.registrationId"); got != "reg-1" {
				t.Errorf("after.payload.registrationId = %#v, want reg-1", got)
			}
			if got := resolve("payload.nested.deep"); got != "yes" {
				t.Errorf("payload.nested.deep = %#v, want yes", got)
			}
		})
	}
}

// A path that stops at the text returns the text. Parsing it there would change
// what `{{.payload}}` binds -- and binding a document where the operator wrote
// a column is not a fix, it is a different bug.
func TestAPathEndingAtJSONTextBindsTheText(t *testing.T) {
	resolve := MessageResolver(msgWithPayload(t, jsonTextPayload))

	got := resolve("payload")
	if s, ok := got.(string); !ok || s != jsonTextPayload {
		t.Errorf("payload = %#v, want the text unchanged", got)
	}
}

// Text that is not JSON must stay unresolved rather than become a panic or a
// half-parsed value.
func TestNonJSONTextDoesNotDescend(t *testing.T) {
	resolve := MessageResolver(msgWithPayload(t, "just a note, not json"))

	if got := resolve("payload.registrationId"); got != nil {
		t.Errorf("payload.registrationId = %#v, want nil", got)
	}
	if got := resolve("payload"); got != "just a note, not json" {
		t.Errorf("payload = %#v, want the text", got)
	}
}

// A real column must always outrank a deeper reading of some other value.
func TestADirectHitStillWins(t *testing.T) {
	m := message.AcquireMessage()
	t.Cleanup(func() { message.ReleaseMessage(m) })
	m.SetData("payload", jsonTextPayload)
	m.SetData("payload.registrationId", "literal-key-wins")

	if got := MessageResolver(m)("payload.registrationId"); got != "literal-key-wins" {
		t.Errorf("got %#v, want the literal key", got)
	}
}
