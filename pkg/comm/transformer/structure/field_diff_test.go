package structure

import (
	"reflect"
	"testing"

	"github.com/gsoultan/hermod"
)

// cdcUpdate is shaped the way the engine shapes a CDC update: the row's
// after-image is the data map and the before-image is on the message.
func cdcUpdate(t *testing.T, before string, after map[string]any) hermod.Message {
	t.Helper()
	msg := newMsg(t, nil)
	msg.SetOperation(hermod.OpUpdate)
	msg.SetTable("orders")
	msg.SetBefore([]byte(before))
	for k, v := range after {
		msg.SetData(k, v)
	}
	return msg
}

func TestFieldDiff(t *testing.T) {
	msg := cdcUpdate(t,
		`{"id":1,"name":"old","qty":2,"meta":{"a":1,"b":2},"updated_at":"t1"}`,
		map[string]any{"id": 1, "name": "new", "qty": 2, "meta": map[string]any{"b": 2, "a": 1}, "updated_at": "t2"},
	)
	out, err := run(t, "field_diff", msg, map[string]any{"ignoreColumns": "updated_at"})
	if err != nil {
		t.Fatalf("field_diff: %v", err)
	}
	data := out.Data()
	// qty is 2 in both images even though one decoded as float64 and the
	// other is an int; meta is the same object in another key order.
	want := map[string]any{"name": map[string]any{"old": "old", "new": "new"}}
	if !reflect.DeepEqual(data["changes"], want) {
		t.Errorf("changes = %#v, want %#v", data["changes"], want)
	}
	if data["name"] != "new" || data["qty"] != 2 {
		t.Errorf("the record itself changed: %#v", data)
	}
}

func TestFieldDiffReadsAnEnvelopeInTheData(t *testing.T) {
	// A Debezium-style body posted to a webhook carries both images as fields.
	msg := newMsg(t, map[string]any{
		"op":     "u",
		"before": map[string]any{"id": 1, "status": "open"},
		"after":  map[string]any{"id": 1, "status": "closed"},
	})
	out, err := run(t, "field_diff", msg, map[string]any{"targetField": "diff"})
	if err != nil {
		t.Fatalf("field_diff: %v", err)
	}
	want := map[string]any{"status": map[string]any{"old": "open", "new": "closed"}}
	if got := out.Data()["diff"]; !reflect.DeepEqual(got, want) {
		t.Errorf("diff = %#v, want %#v", got, want)
	}

	// Only the before-image as a field: the rest of the record is the after-image,
	// and the "before" field is not reported as a column of it.
	msg = newMsg(t, map[string]any{"before": map[string]any{"status": "open"}, "status": "closed"})
	out, err = run(t, "field_diff", msg, map[string]any{"targetField": "diff"})
	if err != nil {
		t.Fatalf("field_diff: %v", err)
	}
	if got := out.Data()["diff"]; !reflect.DeepEqual(got, want) {
		t.Errorf("diff = %#v, want %#v", got, want)
	}
}

func TestFieldDiffOnInsertAndDelete(t *testing.T) {
	ins := newMsg(t, map[string]any{"id": 1})
	ins.SetOperation(hermod.OpCreate)
	out, err := run(t, "field_diff", ins, map[string]any{})
	if err != nil {
		t.Fatalf("field_diff insert: %v", err)
	}
	if want := map[string]any{"id": map[string]any{"old": nil, "new": 1}}; !reflect.DeepEqual(out.Data()["changes"], want) {
		t.Errorf("insert changes = %#v, want %#v", out.Data()["changes"], want)
	}

	del := newMsg(t, nil)
	del.SetOperation(hermod.OpDelete)
	del.SetBefore([]byte(`{"id":1}`))
	out, err = run(t, "field_diff", del, map[string]any{})
	if err != nil {
		t.Fatalf("field_diff delete: %v", err)
	}
	if want := map[string]any{"id": map[string]any{"old": float64(1), "new": nil}}; !reflect.DeepEqual(out.Data()["changes"], want) {
		t.Errorf("delete changes = %#v, want %#v", out.Data()["changes"], want)
	}
}

func TestFieldDiffDropsAnUnchangedRecordWhenAsked(t *testing.T) {
	msg := cdcUpdate(t, `{"id":1,"touched":"a"}`, map[string]any{"id": 1, "touched": "b"})
	out, err := run(t, "field_diff", msg, map[string]any{"ignoreColumns": []any{"touched"}, "dropUnchanged": true})
	if err != nil {
		t.Fatalf("field_diff: %v", err)
	}
	if out != nil {
		t.Errorf("an update that changed only ignored columns was kept: %#v", out.Data())
	}
}

func TestFieldDiffOnlyChanges(t *testing.T) {
	msg := cdcUpdate(t, `{"id":1,"a":"x","b":"y"}`, map[string]any{"id": 1, "a": "x", "b": "z"})
	out, err := run(t, "field_diff", msg, map[string]any{"onlyChanges": "true"})
	if err != nil {
		t.Fatalf("field_diff: %v", err)
	}
	want := map[string]any{"b": map[string]any{"old": "y", "new": "z"}}
	if got := out.Data(); !reflect.DeepEqual(got, want) {
		t.Errorf("data = %#v, want only the changed columns %#v", got, want)
	}
	if out.Operation() != hermod.OpUpdate || out.Table() != "orders" {
		t.Error("the CDC envelope was lost")
	}
}
