package message

import (
	"encoding/json"
	"testing"

	"github.com/gsoultan/hermod"
)

// ToMap is the documented input shape for PopulateFromMap, so the two have to
// be a round trip. They were not: ToMap writes a CDC message's after-image as
// json.RawMessage (jsonRawOrWrapped), while PopulateFromMap only understood a
// map or a string -- so an in-process ToMap -> PopulateFromMap dropped every row
// column silently. Over HTTP it happened to work, because JSON decoding turns
// the raw form into a map on the way in, which is why nothing caught it.
func TestPopulateFromMapRoundTripsToMap(t *testing.T) {
	for _, tc := range []struct {
		name string
		wrap func(map[string]any) map[string]any
	}{
		{"in process", func(m map[string]any) map[string]any { return m }},
		{"through JSON, as the editor sends it", func(m map[string]any) map[string]any {
			b, err := json.Marshal(m)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var out map[string]any
			if err := json.Unmarshal(b, &out); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			return out
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := AcquireMessage()
			defer ReleaseMessage(original)
			original.SetOperation(hermod.Operation("insert"))
			original.SetTable("reminders")
			original.SetData("row_id", "u1")
			original.SetData("payload", map[string]any{"registrantId": "r-1"})

			rebuilt := AcquireMessage()
			defer ReleaseMessage(rebuilt)
			PopulateFromMap(rebuilt, tc.wrap(original.ToMap()))

			data := rebuilt.Data()
			if data["row_id"] != "u1" {
				t.Errorf("row_id = %#v, want u1 -- the after-image must survive the round trip", data["row_id"])
			}
			payload, ok := data["payload"].(map[string]any)
			if !ok || payload["registrantId"] != "r-1" {
				t.Errorf("payload = %#v, want the nested object", data["payload"])
			}
			if rebuilt.Table() != "reminders" {
				t.Errorf("table = %q, want reminders", rebuilt.Table())
			}
			if rebuilt.Operation() != hermod.Operation("insert") {
				t.Errorf("operation = %q, want insert", rebuilt.Operation())
			}
		})
	}
}

// The before-image travels as json.RawMessage too.
func TestPopulateFromMapKeepsARawBeforeImage(t *testing.T) {
	original := AcquireMessage()
	defer ReleaseMessage(original)
	original.SetOperation(hermod.Operation("update"))
	original.SetBefore([]byte(`{"row_id":"u0"}`))
	original.SetData("row_id", "u1")

	rebuilt := AcquireMessage()
	defer ReleaseMessage(rebuilt)
	PopulateFromMap(rebuilt, original.ToMap())

	if got := string(rebuilt.Before()); got != `{"row_id":"u0"}` {
		t.Errorf("before = %s, want the original before-image", got)
	}
}
