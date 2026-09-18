package s3parquet

import (
	"context"
	"strings"
	"testing"

	"github.com/gsoultan/hermod"
)

// A schema that has set a column aside for the CDC operation.
const opTestSchema = `{"Tag":"name=parquet_go_root, repetitiontype=REQUIRED","Fields":[` +
	`{"Tag":"name=id, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=REQUIRED"},` +
	`{"Tag":"name=operation, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=OPTIONAL"}]}`

// The same, but the column is named something else.
const opTestSchemaCustom = `{"Tag":"name=parquet_go_root, repetitiontype=REQUIRED","Fields":[` +
	`{"Tag":"name=id, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=REQUIRED"},` +
	`{"Tag":"name=_op, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=OPTIONAL"}]}`

func newOpSink(t *testing.T, schema, opField string) *S3ParquetSink {
	t.Helper()
	s, err := NewS3ParquetSink(context.Background(), "us-east-1", "b", "p/", "ak", "sk", "", schema, opField, 1)
	if err != nil {
		t.Fatalf("NewS3ParquetSink: %v", err)
	}
	return s
}

// The sink used to write msg.Data() and nothing else, so a delete arrived in the
// file as a row indistinguishable from the insert of the same record. Anything
// reading the file back had no way to tell that the row had been removed
// upstream, and replaying the file re-created rows that no longer existed.
func TestADeleteIsNotFlattenedIntoAnInsert(t *testing.T) {
	s := newOpSink(t, opTestSchema, "")

	row := s.withOperation(map[string]any{"id": "a1"}, hermod.OpDelete)

	if got := row["operation"]; got != string(hermod.OpDelete) {
		t.Errorf("operation column = %v, want %q", got, hermod.OpDelete)
	}
	if got := row["id"]; got != "a1" {
		t.Errorf("the record's own columns must survive: id = %v", got)
	}
}

// Writing a column the schema never declared is a write error for the whole
// batch, so an existing schema has to keep producing exactly the columns it
// always has.
func TestAnOperationColumnIsNotInventedWhenTheSchemaHasNoRoomForIt(t *testing.T) {
	s := newOpSink(t, guardTestSchema, "")

	row := s.withOperation(map[string]any{"id": "a1", "name": "ada"}, hermod.OpDelete)

	if _, ok := row["operation"]; ok {
		t.Errorf("schema declares no operation column, yet one was written: %v", row)
	}
}

// The envelope's operation is a fallback, not an override: a transform that
// computed its own value for the column meant it.
func TestAValueThePipelineComputedOutranksTheEnvelope(t *testing.T) {
	s := newOpSink(t, opTestSchema, "")

	row := s.withOperation(map[string]any{"id": "a1", "operation": "soft_delete"}, hermod.OpDelete)

	if got := row["operation"]; got != "soft_delete" {
		t.Errorf("operation column = %v, want the pipeline's own %q", got, "soft_delete")
	}
}

// A message that never had its operation set is an insert; writing "" into the
// column would be a value no reader can interpret.
func TestAnUnsetOperationIsRecordedAsACreate(t *testing.T) {
	s := newOpSink(t, opTestSchema, "")

	row := s.withOperation(map[string]any{"id": "a1"}, "")

	if got := row["operation"]; got != string(hermod.OpCreate) {
		t.Errorf("operation column = %v, want %q", got, hermod.OpCreate)
	}
}

func TestAnExplicitlyNamedColumnIsUsedInsteadOfTheDefault(t *testing.T) {
	s := newOpSink(t, opTestSchemaCustom, "_op")

	row := s.withOperation(map[string]any{"id": "a1"}, hermod.OpUpdate)

	if got := row["_op"]; got != string(hermod.OpUpdate) {
		t.Errorf("_op column = %v, want %q", got, hermod.OpUpdate)
	}
	if _, ok := row["operation"]; ok {
		t.Errorf("the default column must not also be written: %v", row)
	}
}

// Configuring a column the schema does not have would silently drop every
// operation — the sink was told to record them and would instead write files
// where deletes look like inserts. Refuse at construction, where the operator
// is still looking at the form.
func TestAnExplicitColumnMissingFromTheSchemaIsRefused(t *testing.T) {
	_, err := NewS3ParquetSink(context.Background(), "us-east-1", "b", "p/", "ak", "sk", "", opTestSchema, "_op", 1)
	if err == nil {
		t.Fatal("configuring an operation column absent from the schema must fail")
	}
	if !strings.Contains(err.Error(), "_op") {
		t.Errorf("the error must name the column, got: %v", err)
	}
}

// schemaFieldNames returns nil for a schema it cannot parse, which the sink
// treats as "no opinion" rather than "no columns". The same has to hold here, or
// a schema in an unrecognised shape would make the column unconfigurable.
func TestAnUnparseableSchemaTrustsTheConfiguredColumn(t *testing.T) {
	s := newOpSink(t, "not a schema", "_op")

	row := s.withOperation(map[string]any{"id": "a1"}, hermod.OpDelete)

	if got := row["_op"]; got != string(hermod.OpDelete) {
		t.Errorf("_op column = %v, want %q", got, hermod.OpDelete)
	}
}

func TestWithOperationIsAStillANoOpWhenNoColumnWasSetAside(t *testing.T) {
	s := newOpSink(t, guardTestSchema, "")

	in := map[string]any{"id": "a1", "name": "ada"}
	row := s.withOperation(in, hermod.OpDelete)

	if len(row) != len(in) {
		t.Errorf("row gained columns: %v", row)
	}
}
