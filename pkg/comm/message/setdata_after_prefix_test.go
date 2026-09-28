package message

import (
	"encoding/json"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
)

// A CDC message's data map *is* its after-image: ToMap and MarshalJSON emit it
// as "after", and every reader resolves `after.x` to the column x. The write side
// did not know that. SetData("after.x", v) walked the dotted path like any other
// and created a literal "after" key holding one field -- and from then on that
// one-field map *was* the after-image, because afterImageLocked serialises a
// literal "after" key alone and GetMsgValByPath's `after.` fallback reads that
// same image.
//
// The editor offers every sampled field as `after.<column>`, so a data_conversion
// on `after.scheduled_at`, or a set on `column.after.x`, did this, and every node
// after it lost the row: an api_lookup body's {{.after.user_id}} went out as "",
// the operator's session API refused it as "invalid request body", and each column
// but the one written vanished from what the sink received.
func TestSetDataAfterPrefixWritesIntoTheAfterImage(t *testing.T) {
	const userID = "07581be3-9ecd-5da5-865e-34ab8aae1fec"
	row := func() map[string]any {
		return map[string]any{"user_id": userID, "entity_type": "REGISTRATION", "scheduled_at": "2026-09-27T22:00:00Z"}
	}

	builds := []struct {
		name  string
		build func() *DefaultMessage
	}{
		{"a sample, as the editor's preview builds it", func() *DefaultMessage {
			m := AcquireMessage()
			PopulateFromMap(m, map[string]any{"id": "sample-query-1", "operation": "snapshot", "after": row()})
			return m
		}},
		{"a live CDC event, written before anything read it", func() *DefaultMessage {
			m := AcquireMessage()
			m.SetOperation(hermod.OpUpdate)
			b, _ := json.Marshal(row())
			m.SetAfter(b)
			return m
		}},
		{"a live CDC event that was read first", func() *DefaultMessage {
			m := AcquireMessage()
			m.SetOperation(hermod.OpCreate)
			b, _ := json.Marshal(row())
			m.SetAfter(b)
			_ = m.DataRef()
			return m
		}},
	}

	for _, spelling := range []string{"after.scheduled_at", "$.after.scheduled_at", "After.scheduled_at"} {
		for _, tc := range builds {
			t.Run(spelling+"/"+tc.name, func(t *testing.T) {
				m := tc.build()
				defer ReleaseMessage(m)

				m.SetData(spelling, "2026-09-28")

				if _, literal := m.Data()["after"]; literal {
					t.Fatalf("SetData(%q) created a literal \"after\" key, which replaces the after-image: %v", spelling, m.Data())
				}
				// The written value is the row's column, where the read side looks.
				for _, path := range []string{"scheduled_at", "after.scheduled_at"} {
					if got := evaluator.GetMsgValByPath(m, path); got != "2026-09-28" {
						t.Errorf("%s = %#v after writing %q, want the written value", path, got, spelling)
					}
				}
				// And the rest of the row is still there.
				if got := evaluator.GetMsgValByPath(m, "after.user_id"); got != userID {
					t.Errorf("after.user_id = %#v after writing %q; the write dropped the rest of the row", got, spelling)
				}
				if got, _ := evaluator.ResolveJSONTemplateMsg(`{"created_by_id":"{{.after.user_id}}"}`, m); got != `{"created_by_id":"`+userID+`"}` {
					t.Errorf("an api_lookup body after the write resolved to %s", got)
				}
				// Every serialisation carries the whole row plus the write.
				after, _ := m.ToMap()["after"].(json.RawMessage)
				var image map[string]any
				if err := json.Unmarshal(after, &image); err != nil {
					t.Fatalf("ToMap after-image %s: %v", after, err)
				}
				if image["user_id"] != userID || image["scheduled_at"] != "2026-09-28" || image["entity_type"] != "REGISTRATION" {
					t.Errorf("ToMap after-image lost the row: %v", image)
				}
				raw, err := m.MarshalJSON()
				if err != nil {
					t.Fatalf("MarshalJSON: %v", err)
				}
				var wire struct {
					After map[string]any `json:"after"`
				}
				if err := json.Unmarshal(raw, &wire); err != nil {
					t.Fatalf("decode %s: %v", raw, err)
				}
				if wire.After["user_id"] != userID || wire.After["scheduled_at"] != "2026-09-28" {
					t.Errorf("what a sink receives lost the row: %s", raw)
				}
			})
		}
	}
}

// A row can have a column that is really called "after", and the read side lets
// a real column win over the envelope: `after.x` then means that column's x. The
// write side keeps agreeing with it, so the value lands inside the column.
func TestSetDataAfterPrefixWritesIntoARealAfterColumn(t *testing.T) {
	m := AcquireMessage()
	defer ReleaseMessage(m)
	PopulateFromMap(m, map[string]any{
		"operation": "update",
		"after":     map[string]any{"id": "1", "after": map[string]any{"state": "shipped"}},
	})

	m.SetData("after.carrier", "DHL")

	column, ok := m.Data()["after"].(map[string]any)
	if !ok || column["carrier"] != "DHL" || column["state"] != "shipped" {
		t.Fatalf("the row's own \"after\" column = %#v, want the write inside it next to state", m.Data()["after"])
	}
	if _, stray := m.Data()["carrier"]; stray {
		t.Errorf("the write also landed at the root: %v", m.Data())
	}
	if got := evaluator.GetMsgValByPath(m, "after.carrier"); got != "DHL" {
		t.Errorf("after.carrier = %#v, want DHL", got)
	}
}

// Without an operation a message is not a CDC event and has no after-image; an
// "after" key there is an ordinary nested object, so the write nests as before.
func TestSetDataAfterPrefixOnANonCDCMessageNests(t *testing.T) {
	m := AcquireMessage()
	defer ReleaseMessage(m)
	m.SetData("user_id", "u-1")

	m.SetData("after.status", "done")

	nested, ok := m.Data()["after"].(map[string]any)
	if !ok || nested["status"] != "done" {
		t.Fatalf(`data["after"] = %#v, want {"status":"done"}`, m.Data()["after"])
	}
	if _, stray := m.Data()["status"]; stray {
		t.Errorf("a non-CDC write was moved to the root: %v", m.Data())
	}
}
