package s3parquet

import (
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

const guardTestSchema = `{"Tag":"name=parquet_go_root, repetitiontype=REQUIRED","Fields":[` +
	`{"Tag":"name=id, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=REQUIRED"},` +
	`{"Tag":"name=name, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=REQUIRED"}]}`

func TestSchemaFieldNames(t *testing.T) {
	got := schemaFieldNames(guardTestSchema)
	for _, want := range []string{"id", "name"} {
		if _, ok := got[want]; !ok {
			t.Errorf("schema column %q not found in %v", want, got)
		}
	}
	if _, ok := got["parquet_go_root"]; ok {
		t.Errorf("the root tag is not a column: %v", got)
	}
	if n := len(got); n != 2 {
		t.Errorf("got %d columns, want 2: %v", n, got)
	}
	if schemaFieldNames("not a schema") != nil {
		t.Error("an unparseable schema should yield nil, meaning no opinion")
	}
}

// A message whose payload is not a JSON object decodes to a single synthetic
// "payload" field. That is a real field, so testing Data() for emptiness passes
// the record through to the parquet writer, which then fails the whole batch in
// WriteStop with "interface conversion" — naming neither the record nor the
// reason. The guard has to measure the record against the schema's columns.
func TestWritableFieldCount_NonObjectPayloadHasNothingForTheSchema(t *testing.T) {
	fields := schemaFieldNames(guardTestSchema)

	bad := message.AcquireMessage()
	defer message.ReleaseMessage(bad)
	bad.SetID("bad")
	bad.SetOperation(hermod.OpCreate)
	bad.SetPayload([]byte("this is not json"))

	data := bad.Data()
	if len(data) == 0 {
		t.Fatal("precondition: a non-object payload should decode to a synthetic field, " +
			"otherwise this test is not exercising the case it describes")
	}
	if n := writableFieldCount(data, fields); n != 0 {
		t.Errorf("writableFieldCount = %d, want 0; data %v carries no schema column", n, data)
	}
}

func TestWritableFieldCount_GoodRecordCountsItsColumns(t *testing.T) {
	fields := schemaFieldNames(guardTestSchema)

	good := message.AcquireMessage()
	defer message.ReleaseMessage(good)
	good.SetID("good")
	good.SetPayload([]byte(`{"id":"good","name":"ada","extra":"ignored"}`))

	if n := writableFieldCount(good.Data(), fields); n != 2 {
		t.Errorf("writableFieldCount = %d, want 2 (id and name)", n)
	}
}

func TestWritableFieldCount_UnknownSchemaFallsBackToPresence(t *testing.T) {
	// With no parseable schema the guard must not refuse everything; it falls
	// back to "does the record carry anything at all".
	if n := writableFieldCount(map[string]any{"whatever": 1}, nil); n != 1 {
		t.Errorf("writableFieldCount = %d, want 1 when the schema shape is unknown", n)
	}
	if n := writableFieldCount(map[string]any{}, nil); n != 0 {
		t.Errorf("writableFieldCount = %d, want 0 for an empty record", n)
	}
}
