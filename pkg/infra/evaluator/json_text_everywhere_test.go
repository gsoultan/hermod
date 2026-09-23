package evaluator

import (
	"encoding/json"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// MessageResolver learned to read a column holding JSON text, because a SQL
// template needed it. Nothing else did, so one Hermod read the same column two
// ways: `{{.payload.id}}` resolved in a db_lookup query and stayed empty in the
// condition, the sink mapping and the email template beside it.
//
// GetValByPath is where that has to be fixed rather than at each caller. It is
// the one function GetMsgValByPath consults first and the one every map-form
// template resolves through, so conditions, routers, filters, mask/encrypt,
// foreach and every sink mapping reach a column through it.

const textColumn = `{"id":"reg-1","nested":{"deep":"yes"},"n":7}`

func textColumnData() map[string]any {
	return map[string]any{"payload": textColumn, "plain": "hello"}
}

func TestGetValByPathDescendsIntoAJSONTextColumn(t *testing.T) {
	data := textColumnData()

	if got := GetValByPath(data, "payload.id"); got != "reg-1" {
		t.Errorf("payload.id = %#v, want reg-1", got)
	}
	if got := GetValByPath(data, "payload.nested.deep"); got != "yes" {
		t.Errorf("payload.nested.deep = %#v, want yes", got)
	}
}

// Every map-form template goes through resolveTemplatePath -> GetValByPath, so
// a notification body, an SMTP subject and a sink mapping all follow.
func TestResolveTemplateDescendsIntoAJSONTextColumn(t *testing.T) {
	if got := ResolveTemplate("{{.payload.id}}", textColumnData()); got != "reg-1" {
		t.Errorf("template = %q, want reg-1", got)
	}
}

// The message form has to agree with the map form, or a condition and a sink
// mapping on the same field disagree again.
func TestGetMsgValByPathDescendsIntoAJSONTextColumn(t *testing.T) {
	m := message.AcquireMessage()
	t.Cleanup(func() { message.ReleaseMessage(m) })
	m.SetOperation(hermod.Operation("insert"))
	m.SetData("payload", textColumn)

	for _, p := range []string{"payload.id", "after.payload.id"} {
		if got := GetMsgValByPath(m, p); got != "reg-1" {
			t.Errorf("%s = %#v, want reg-1", p, got)
		}
	}
}

// The same three guards the SQL-template descent carries, restated here because
// this path is reached by far more callers.
func TestTheMapDescentKeepsItsGuards(t *testing.T) {
	t.Run("a path ending at the text binds the text", func(t *testing.T) {
		if got := GetValByPath(textColumnData(), "payload"); got != textColumn {
			t.Errorf("payload = %#v, want the text unchanged", got)
		}
	})

	t.Run("text that is not JSON does not descend", func(t *testing.T) {
		data := map[string]any{"note": "just a note, not json"}
		if got := GetValByPath(data, "note.id"); got != nil {
			t.Errorf("note.id = %#v, want nil", got)
		}
	})

	t.Run("the descent is a last resort, not a first guess", func(t *testing.T) {
		// Both columns carry an "id". The one the path names is a decoded
		// object, so it answers; the text column beside it must not be
		// consulted. (A literal key spelled "payload.id" is unreachable by any
		// dotted-path resolver here, and always has been -- that is not what
		// this guards.)
		data := map[string]any{
			"payload": map[string]any{"id": "from-the-object"},
			"other":   `{"id":"from-the-text"}`,
		}
		if got := GetValByPath(data, "payload.id"); got != "from-the-object" {
			t.Errorf("payload.id = %#v, want from-the-object", got)
		}
	})

	t.Run("a decoded object is unaffected", func(t *testing.T) {
		data := map[string]any{"payload": map[string]any{"id": "reg-1"}}
		if got := GetValByPath(data, "payload.id"); got != "reg-1" {
			t.Errorf("payload.id = %#v, want reg-1", got)
		}
	})
}

// []byte and json.RawMessage reach the map from drivers that hand a JSON column
// over as bytes.
func TestTheMapDescentReadsBytesToo(t *testing.T) {
	for _, tc := range []struct {
		name string
		val  any
	}{
		{"[]byte", []byte(textColumn)},
		{"json.RawMessage", json.RawMessage(textColumn)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := GetValByPath(map[string]any{"payload": tc.val}, "payload.id"); got != "reg-1" {
				t.Errorf("payload.id = %#v, want reg-1", got)
			}
		})
	}
}
