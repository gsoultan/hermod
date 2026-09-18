package file

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/xitongsys/parquet-go-source/local"
	"github.com/xitongsys/parquet-go/writer"
)

const pqChangeSchema = `{"Tag":"name=parquet_go_root, repetitiontype=REQUIRED","Fields":[` +
	`{"Tag":"name=id, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=REQUIRED"},` +
	`{"Tag":"name=name, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=OPTIONAL"},` +
	`{"Tag":"name=qty, type=INT64, repetitiontype=REQUIRED"},` +
	`{"Tag":"name=operation, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=REQUIRED"}]}`

const pqPlainSchema = `{"Tag":"name=parquet_go_root, repetitiontype=REQUIRED","Fields":[` +
	`{"Tag":"name=id, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=REQUIRED"},` +
	`{"Tag":"name=qty, type=INT64, repetitiontype=REQUIRED"}]}`

// writeParquet lays down a real parquet file using the same writer the
// s3parquet sink uses, so these tests prove the two ends agree rather than
// agreeing with a fixture written by hand.
func writeParquet(t *testing.T, schema string, rows ...string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "changes.parquet")

	fw, err := local.NewLocalFileWriter(path)
	if err != nil {
		t.Fatalf("local writer: %v", err)
	}
	pw, err := writer.NewJSONWriter(schema, fw, 1)
	if err != nil {
		t.Fatalf("parquet writer: %v", err)
	}
	for _, r := range rows {
		if err := pw.Write(r); err != nil {
			t.Fatalf("write row %s: %v", r, err)
		}
	}
	if err := pw.WriteStop(); err != nil {
		t.Fatalf("write stop: %v", err)
	}
	if err := fw.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return dir
}

func parquetSource(t *testing.T, dir string, cfg GenericConfig) *GenericFileSource {
	t.Helper()
	cfg.Backend = BackendLocal
	cfg.LocalPath = dir
	cfg.Pattern = "*.parquet"
	cfg.Format = FormatParquet
	s := NewGenericFileSource(cfg)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// The point of the whole connector: a parquet file that carries a CDC operation
// per row drives insert, update and delete downstream instead of landing as
// three indistinguishable inserts.
func TestAParquetFileDrivesInsertUpdateAndDelete(t *testing.T) {
	dir := writeParquet(t, pqChangeSchema,
		`{"id":"a1","name":"ada","qty":1,"operation":"create"}`,
		`{"id":"a2","name":"bob","qty":2,"operation":"update"}`,
		`{"id":"a3","name":"cyd","qty":3,"operation":"delete"}`,
	)
	s := parquetSource(t, dir, GenericConfig{KeyField: "id", OpField: "operation"})

	want := []struct {
		id   string
		op   hermod.Operation
		name string
		qty  int64
	}{
		{"a1", hermod.OpCreate, "ada", 1},
		{"a2", hermod.OpUpdate, "bob", 2},
		{"a3", hermod.OpDelete, "cyd", 3},
	}
	for i, w := range want {
		msg, err := s.Read(t.Context())
		if err != nil {
			t.Fatalf("row %d: %v", i, err)
		}
		if msg == nil {
			t.Fatalf("row %d: no message", i)
		}
		if msg.ID() != w.id {
			t.Errorf("row %d: ID = %q, want %q — a sink targets the row by this", i, msg.ID(), w.id)
		}
		if msg.Operation() != w.op {
			t.Errorf("row %d: operation = %q, want %q", i, msg.Operation(), w.op)
		}
		data := msg.Data()
		if data["name"] != w.name {
			t.Errorf("row %d: name = %v, want %q", i, data["name"], w.name)
		}
		if data["qty"] != w.qty {
			t.Errorf("row %d: qty = %#v, want int64 %d — an INT64 column must not arrive as a string", i, data["qty"], w.qty)
		}
	}
}

// The operation column is consumed, not passed through: its meaning now lives
// in msg.Operation(). Leaving it in the record would make a database sink try
// to write an "operation" column the target table does not have, and the
// s3parquet sink materialises it back from the envelope anyway.
func TestTheOperationColumnIsConsumedNotPassedThrough(t *testing.T) {
	dir := writeParquet(t, pqChangeSchema, `{"id":"a1","name":"ada","qty":1,"operation":"delete"}`)
	s := parquetSource(t, dir, GenericConfig{KeyField: "id", OpField: "operation"})

	msg, err := s.Read(t.Context())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if _, ok := msg.Data()["operation"]; ok {
		t.Errorf("the operation column is still in the record: %v", msg.Data())
	}
	if _, ok := msg.Data()["id"]; !ok {
		t.Errorf("the key column is real data and must stay: %v", msg.Data())
	}
}

// A file with no operation column is a plain data file, which is every row an
// insert — not an error.
func TestAFileWithNoOperationColumnIsAllInserts(t *testing.T) {
	dir := writeParquet(t, pqPlainSchema, `{"id":"a1","qty":1}`, `{"id":"a2","qty":2}`)
	s := parquetSource(t, dir, GenericConfig{KeyField: "id", OpField: "operation"})

	for i := range 2 {
		msg, err := s.Read(t.Context())
		if err != nil {
			t.Fatalf("row %d: %v", i, err)
		}
		if msg.Operation() != hermod.OpCreate {
			t.Errorf("row %d: operation = %q, want %q", i, msg.Operation(), hermod.OpCreate)
		}
	}
}

// A value the mapping does not recognise must stop the read. Falling back to
// "insert" would turn a misspelt delete into a row that silently comes back.
func TestAnUnrecognisedOperationIsRefused(t *testing.T) {
	dir := writeParquet(t, pqChangeSchema, `{"id":"a1","name":"ada","qty":1,"operation":"DELTE"}`)
	s := parquetSource(t, dir, GenericConfig{KeyField: "id", OpField: "operation"})

	_, err := s.Read(t.Context())
	if err == nil {
		t.Fatal("an unrecognised operation must be an error, not an insert")
	}
	if !strings.Contains(err.Error(), "DELTE") {
		t.Errorf("the error must quote the offending value, got: %v", err)
	}
}

// Sinks without column mappings target by msg.ID(). A synthetic per-row ID
// would make every delete a DELETE that matches nothing and still reports
// success, so refuse rather than produce one.
func TestAnUpdateOrDeleteWithoutAKeyFieldIsRefused(t *testing.T) {
	for _, op := range []string{"update", "delete"} {
		t.Run(op, func(t *testing.T) {
			dir := writeParquet(t, pqChangeSchema,
				`{"id":"a1","name":"ada","qty":1,"operation":"`+op+`"}`)
			s := parquetSource(t, dir, GenericConfig{OpField: "operation"})

			_, err := s.Read(t.Context())
			if err == nil {
				t.Fatalf("a %s with no key_field must be refused", op)
			}
			if !strings.Contains(err.Error(), "key_field") {
				t.Errorf("the error must name the missing setting, got: %v", err)
			}
		})
	}
}

// Without an operation column there is nothing to target by, so a file of pure
// inserts stays usable with no key_field configured.
func TestInsertsDoNotNeedAKeyField(t *testing.T) {
	dir := writeParquet(t, pqPlainSchema, `{"id":"a1","qty":1}`)
	s := parquetSource(t, dir, GenericConfig{})

	msg, err := s.Read(t.Context())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if msg.ID() == "" {
		t.Error("a message still needs an ID")
	}
}

// A NULL in an optional column is absent, not the empty string: a sink writing
// "" over a real value is data loss.
func TestANullColumnIsNil(t *testing.T) {
	dir := writeParquet(t, pqChangeSchema, `{"id":"a1","qty":1,"operation":"create"}`)
	s := parquetSource(t, dir, GenericConfig{KeyField: "id", OpField: "operation"})

	msg, err := s.Read(t.Context())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	v, ok := msg.Data()["name"]
	if !ok {
		t.Fatalf("the NULL column was dropped entirely, so a sink cannot write NULL over a stale value: %v", msg.Data())
	}
	if v != nil {
		t.Errorf("a NULL optional column came through as %#v", v)
	}
}

// Sample feeds the editor's field list. It must not consume the row the
// pipeline is about to read.
func TestSampleReadsARowWithoutConsumingIt(t *testing.T) {
	dir := writeParquet(t, pqChangeSchema,
		`{"id":"a1","name":"ada","qty":1,"operation":"create"}`,
		`{"id":"a2","name":"bob","qty":2,"operation":"update"}`,
	)
	s := parquetSource(t, dir, GenericConfig{KeyField: "id", OpField: "operation"})

	sample, err := s.Sample(t.Context(), "")
	if err != nil {
		t.Fatalf("sample: %v", err)
	}
	if sample.ID() != "a1" {
		t.Errorf("sample ID = %q, want the first row", sample.ID())
	}
	if _, ok := sample.Data()["name"]; !ok {
		t.Errorf("the sample must carry the columns the editor lists: %v", sample.Data())
	}

	first, err := s.Read(t.Context())
	if err != nil {
		t.Fatalf("read after sample: %v", err)
	}
	if first.ID() != "a1" {
		t.Errorf("sampling consumed a row: first read = %q, want a1", first.ID())
	}
}

func TestParseOperation(t *testing.T) {
	cases := []struct {
		in   any
		want hermod.Operation
		err  bool
	}{
		{"create", hermod.OpCreate, false},
		{"insert", hermod.OpCreate, false},
		{"c", hermod.OpCreate, false},
		{"I", hermod.OpCreate, false},
		{"update", hermod.OpUpdate, false},
		{"upsert", hermod.OpUpdate, false},
		{"u", hermod.OpUpdate, false},
		{"delete", hermod.OpDelete, false},
		{"d", hermod.OpDelete, false},
		{" Delete ", hermod.OpDelete, false},
		{"read", hermod.OpSnapshot, false},
		{"r", hermod.OpSnapshot, false},
		{"snapshot", hermod.OpSnapshot, false},
		{nil, hermod.OpCreate, false},
		{"", hermod.OpCreate, false},
		{[]byte("delete"), hermod.OpDelete, false},
		{"merge", "", true},
		{7, "", true},
	}
	for _, c := range cases {
		got, err := parseOperation(c.in)
		if c.err {
			if err == nil {
				t.Errorf("parseOperation(%#v) = %q, want an error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseOperation(%#v): %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseOperation(%#v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// "operation" is a plausible name for a column that has nothing to do with CDC.
// Because the default applies whenever the column is present, such a file read
// as "unrecognised operation" on row 1 with no way to say otherwise — the
// column could be renamed, but not ignored.
func TestTheOperationColumnCanBeTurnedOff(t *testing.T) {
	dir := writeParquet(t, pqChangeSchema, `{"id":"a1","name":"ada","qty":1,"operation":"appendectomy"}`)
	s := parquetSource(t, dir, GenericConfig{KeyField: "id", OpField: "-"})

	msg, err := s.Read(t.Context())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if msg.Operation() != hermod.OpCreate {
		t.Errorf("operation = %q, want %q", msg.Operation(), hermod.OpCreate)
	}
	if got := msg.Data()["operation"]; got != "appendectomy" {
		t.Errorf("the column is ordinary data now and must survive: %v", got)
	}
}
