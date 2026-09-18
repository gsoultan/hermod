package core

import (
	"reflect"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/pkg/comm/message"
)

// convertMulti drives the node with a `conversions` list shaped the way stored
// config arrives: the workflow is persisted as JSON, so a row is a
// map[string]any inside an []any, never a typed Go struct. A test that builds
// typed rows would pass against code the engine never reaches.
func convertMulti(t *testing.T, in map[string]any, cfg map[string]any) (map[string]any, error) {
	t.Helper()
	m := message.AcquireMessage()
	for k, v := range in {
		m.SetData(k, v)
	}
	tr := &DataConversionTransformer{}
	out, err := tr.Transform(t.Context(), m, cfg)
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, nil
	}
	return out.Data(), nil
}

func TestDataConversion_MultipleFieldsDifferentTargetTypes(t *testing.T) {
	data, err := convertMulti(t, map[string]any{
		"amount":     "12.5",
		"qty":        "7",
		"active":     "true",
		"tags":       "a|b",
		"created_at": "2024-03-01",
	}, map[string]any{
		"conversions": []any{
			map[string]any{"field": "amount", "targetType": "float"},
			map[string]any{"field": "qty", "targetType": "int"},
			map[string]any{"field": "active", "targetType": "bool"},
			map[string]any{"field": "tags", "targetType": "array", "separator": "|", "elementType": "string"},
			map[string]any{"field": "created_at", "targetType": "date", "format": "2006-01-02"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := data["amount"]; got != 12.5 {
		t.Errorf("amount = %#v, want 12.5", got)
	}
	if got := data["qty"]; got != int64(7) {
		t.Errorf("qty = %#v, want int64(7)", got)
	}
	if got := data["active"]; got != true {
		t.Errorf("active = %#v, want true", got)
	}
	if got, want := data["tags"], []any{"a", "b"}; !reflect.DeepEqual(got, want) {
		t.Errorf("tags = %#v, want %#v", got, want)
	}
	if data["created_at"] == "2024-03-01" {
		t.Errorf("created_at was not converted, still the input string")
	}
}

// A row's own errorBehavior wins over the node's. The case this exists for is a
// pipeline that must fail on a bad key column while letting an optional column
// go null -- previously only reachable by splitting into two nodes.
func TestDataConversion_MultiField_RowErrorBehaviorOverridesNodeDefault(t *testing.T) {
	data, err := convertMulti(t, map[string]any{
		"amount": "12.5",
		"note":   "not a number",
	}, map[string]any{
		"errorBehavior": "fail",
		"conversions": []any{
			map[string]any{"field": "amount", "targetType": "float"},
			map[string]any{"field": "note", "targetType": "int", "errorBehavior": "null"},
		},
	})
	if err != nil {
		t.Fatalf("row override should have absorbed the failure, got: %v", err)
	}
	if got := data["amount"]; got != 12.5 {
		t.Errorf("amount = %#v, want 12.5", got)
	}
	if got, ok := data["note"]; !ok || got != nil {
		t.Errorf("note = %#v (present=%v), want an explicit nil", got, ok)
	}
}

// A row with no errorBehavior of its own inherits the node's.
func TestDataConversion_MultiField_RowInheritsNodeErrorBehavior(t *testing.T) {
	data, err := convertMulti(t, map[string]any{
		"amount": "12.5",
		"note":   "not a number",
	}, map[string]any{
		"errorBehavior": "keep",
		"conversions": []any{
			map[string]any{"field": "amount", "targetType": "float"},
			map[string]any{"field": "note", "targetType": "int"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := data["note"]; got != "not a number" {
		t.Errorf("note = %#v, want the original value kept", got)
	}
}

// A failing row aborts the whole node and leaves the message untouched.
//
// Writes are staged and applied only once every row has resolved, because the
// engine forwards the *input* message when a workflow sets onError "continue"
// (internal/engine/registry/registry.go). Converting in place would have sent a
// half-converted row on to the sink under exactly that setting -- some columns
// retyped, the one that failed still raw -- with nothing downstream able to tell.
func TestDataConversion_MultiField_FailingRowWritesNothing(t *testing.T) {
	m := message.AcquireMessage()
	m.SetData("amount", "12.5")
	m.SetData("qty", "not a number")

	tr := &DataConversionTransformer{}
	out, err := tr.Transform(t.Context(), m, map[string]any{
		"errorBehavior": "fail",
		"conversions": []any{
			map[string]any{"field": "amount", "targetType": "float"},
			map[string]any{"field": "qty", "targetType": "int"},
		},
	})
	if err == nil {
		t.Fatal("want an error from the failing row, got nil")
	}
	if out != nil {
		t.Errorf("want a nil message alongside the error, got %#v", out)
	}
	if got := m.Data()["amount"]; got != "12.5" {
		t.Errorf("amount = %#v on the input message: an earlier row was written before a later row failed", got)
	}
}

// The row order in the config is the write order. Stored config is a JSON
// array, so the order survives the round trip -- unlike the `set` node, whose
// columns live in a map and had to be sorted by path to stop two overlapping
// writes from landing differently run to run.
func TestDataConversion_MultiField_RowsAppliedInConfiguredOrder(t *testing.T) {
	cfg := map[string]any{
		"conversions": []any{
			map[string]any{"field": "v", "targetType": "int", "targetField": "out"},
			map[string]any{"field": "v", "targetType": "string", "targetField": "out"},
		},
	}
	for i := 0; i < 50; i++ {
		data, err := convertMulti(t, map[string]any{"v": "7"}, cfg)
		if err != nil {
			t.Fatalf("iteration %d: unexpected error: %v", i, err)
		}
		if got := data["out"]; got != "7" {
			t.Fatalf("iteration %d: out = %#v, want the last row to win with \"7\"", i, got)
		}
	}
}

// Each row reads the message as it arrived, not as an earlier row left it, so
// two rows reading the same field agree no matter where they sit in the list.
func TestDataConversion_MultiField_RowsReadTheIncomingMessage(t *testing.T) {
	data, err := convertMulti(t, map[string]any{"v": "7"}, map[string]any{
		"conversions": []any{
			map[string]any{"field": "v", "targetType": "int"},
			map[string]any{"field": "v", "targetType": "string", "targetField": "v_text"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := data["v"]; got != int64(7) {
		t.Errorf("v = %#v, want int64(7)", got)
	}
	if got := data["v_text"]; got != "7" {
		t.Errorf("v_text = %#v, want \"7\" read from the incoming value", got)
	}
}

func TestDataConversion_MultiField_RowTargetFieldLeavesSourceAlone(t *testing.T) {
	data, err := convertMulti(t, map[string]any{"qty": "7"}, map[string]any{
		"conversions": []any{
			map[string]any{"field": "qty", "targetType": "int", "targetField": "qty_int"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := data["qty"]; got != "7" {
		t.Errorf("qty = %#v, want the source field untouched", got)
	}
	if got := data["qty_int"]; got != int64(7) {
		t.Errorf("qty_int = %#v, want int64(7)", got)
	}
}

// An unknown target type is a configuration fault, not a value fault, so it is
// reported even when the row would otherwise swallow errors.
func TestDataConversion_MultiField_UnsupportedTargetTypeIsNotSwallowed(t *testing.T) {
	_, err := convertMulti(t, map[string]any{"amount": "1"}, map[string]any{
		"conversions": []any{
			map[string]any{"field": "amount", "targetType": "banana", "errorBehavior": "null"},
		},
	})
	if err == nil {
		t.Fatal("want an error for an unsupported target type, got nil")
	}
	if !strings.Contains(err.Error(), "banana") {
		t.Errorf("error %q does not name the offending target type", err)
	}
}

// A missing field is a conversion failure like any other and follows the row's
// behaviour -- "keep" has nothing to keep, so it writes nothing at all rather
// than inventing the field as null.
func TestDataConversion_MultiField_MissingFieldFollowsRowBehavior(t *testing.T) {
	data, err := convertMulti(t, map[string]any{"amount": "1"}, map[string]any{
		"conversions": []any{
			map[string]any{"field": "amount", "targetType": "float"},
			map[string]any{"field": "nope", "targetType": "int", "errorBehavior": "keep"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := data["nope"]; ok {
		t.Errorf("nope = %#v, want no field written at all", data["nope"])
	}

	if _, err := convertMulti(t, map[string]any{"amount": "1"}, map[string]any{
		"conversions": []any{
			map[string]any{"field": "nope", "targetType": "int", "errorBehavior": "fail"},
		},
	}); err == nil {
		t.Fatal("want an error for a field that resolves to nothing under \"fail\"")
	}
}

// A row with no field is skipped rather than failing the node: the editor adds
// an empty row the moment the operator clicks Add, and a half-filled row should
// not turn every message red while they are still typing.
func TestDataConversion_MultiField_RowWithNoFieldIsSkipped(t *testing.T) {
	data, err := convertMulti(t, map[string]any{"amount": "12.5"}, map[string]any{
		"errorBehavior": "fail",
		"conversions": []any{
			map[string]any{"field": "", "targetType": "int"},
			map[string]any{"field": "amount", "targetType": "float"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := data["amount"]; got != 12.5 {
		t.Errorf("amount = %#v, want 12.5", got)
	}
}

// Configs stored before the node grew a row list keep working unchanged.
func TestDataConversion_MultiField_LegacySingleFieldConfigStillWorks(t *testing.T) {
	data, err := convertMulti(t, map[string]any{"qty": "7"}, map[string]any{
		"field":      "qty",
		"targetType": "int",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := data["qty"]; got != int64(7) {
		t.Errorf("qty = %#v, want int64(7)", got)
	}
}

// An empty row list means the node converts nothing. The editor leaves the
// pre-list keys in place, so falling back to them here would have resurrected
// the conversion the operator deleted the last row to get rid of.
func TestDataConversion_MultiField_EmptyRowListConvertsNothing(t *testing.T) {
	data, err := convertMulti(t, map[string]any{"qty": "7"}, map[string]any{
		"field":       "qty",
		"targetType":  "int",
		"conversions": []any{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := data["qty"]; got != "7" {
		t.Errorf("qty = %#v, want the value left alone", got)
	}
}

// The engine calls Prepare once at workflow build time and Transform per
// message, so the node has two ways to reach its rows: the cached parse and the
// fallback the editor's preview endpoint takes. They have to agree. A node that
// converts one way on the canvas and another in the pipeline is the defect this
// covers, and it is invisible from either side alone.
func TestDataConversion_MultiField_PreparedAndUnpreparedAgree(t *testing.T) {
	cfg := map[string]any{
		"errorBehavior": "fail",
		"conversions": []any{
			map[string]any{"field": "amount", "targetType": "float"},
			map[string]any{"field": "qty", "targetType": "int", "targetField": "qty_int"},
			map[string]any{"field": "note", "targetType": "int", "errorBehavior": "null"},
		},
	}
	in := map[string]any{"amount": "12.5", "qty": "7", "note": "not a number"}

	unprepared, err := convertMulti(t, in, cfg)
	if err != nil {
		t.Fatalf("unprepared: %v", err)
	}

	tr := &DataConversionTransformer{}
	preparedCfg, err := tr.Prepare(cfg)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if _, ok := preparedCfg["_parsed_conversions"].([]conversionRow); !ok {
		t.Fatal("Prepare did not cache the parsed rows, so every message re-parses the config")
	}
	prepared, err := convertMulti(t, in, preparedCfg)
	if err != nil {
		t.Fatalf("prepared: %v", err)
	}

	if !reflect.DeepEqual(prepared, unprepared) {
		t.Errorf("prepared = %#v\nunprepared = %#v", prepared, unprepared)
	}
	if got := prepared["amount"]; got != 12.5 {
		t.Errorf("amount = %#v, want 12.5", got)
	}
	if got := prepared["qty_int"]; got != int64(7) {
		t.Errorf("qty_int = %#v, want int64(7)", got)
	}
	if got, ok := prepared["note"]; !ok || got != nil {
		t.Errorf("note = %#v (present=%v), want an explicit nil", got, ok)
	}
}
