package http

import (
	"testing"

	_ "github.com/gsoultan/hermod/pkg/comm/transformer/core"
)

// A CDC sample posted to the preview endpoint comes back with its after-image
// under "after" and its system fields at the root -- the shape a real CDC
// message has. It used to come back with the system fields in *both* places:
//
//	{"operation":"create","table":"orders",
//	 "after":{"operation":"create","table":"orders","user_id":1}}
//
// because populateMessageFromMap copied operation/table/schema into the data map
// "for convenience in transformations", and ToMap marshals the whole data map as
// the after-image when there is no payload. A live CDC source sets the payload
// (SetAfter), so only messages built field-by-field -- which is to say, only the
// preview -- ever showed this.
//
// The convenience was never needed: evaluator.GetMsgValByPath exposes
// operation/op/table/schema as virtual fields resolved from the message itself,
// and deliberately lets a real data column of the same name win over them.
func TestPreview_CDCSampleDoesNotEchoSystemFieldsIntoAfter(t *testing.T) {
	sample := map[string]any{
		"operation": "create",
		"table":     "orders",
		"schema":    "public",
		"after":     map[string]any{"user_id": 1, "amount": 10},
	}

	resp := postTransformation(t, map[string]any{
		"field":       "user_id",
		"mapping":     `{"1":"ada"}`,
		"mappingType": "exact",
		"targetField": "owner",
	}, "mapping", sample)

	// The root still describes the change.
	for field, want := range map[string]any{"operation": "create", "table": "orders", "schema": "public"} {
		if got := resp[field]; got != want {
			t.Errorf("root %q = %#v, want %#v (response: %#v)", field, got, want, resp)
		}
	}

	after, ok := resp["after"].(map[string]any)
	if !ok {
		t.Fatalf("after is %T, want an object (response: %#v)", resp["after"], resp)
	}

	// The after-image is the row, not the row plus a copy of the envelope.
	for _, field := range []string{"operation", "table", "schema"} {
		if _, echoed := after[field]; echoed {
			t.Errorf("after[%q] = %#v -- the envelope was copied into the after-image; "+
				"the panel shows every system field twice (after: %#v)", field, after[field], after)
		}
	}

	// And the row itself, including what the transformation wrote, is intact.
	if after["user_id"] != float64(1) {
		t.Errorf("after[\"user_id\"] = %#v, want 1 (after: %#v)", after["user_id"], after)
	}
	if after["owner"] != "ada" {
		t.Errorf("after[\"owner\"] = %#v, want %q -- the transformation's output is missing (after: %#v)",
			after["owner"], "ada", after)
	}
}

// The virtual fields are what makes the change above safe: a transformation
// addressing "table" or "operation" must still resolve, even though those are no
// longer copied into the data map.
func TestPreview_CDCSystemFieldsAreStillAddressable(t *testing.T) {
	sample := map[string]any{
		"operation": "update",
		"table":     "orders",
		"after":     map[string]any{"user_id": 1},
	}

	resp := postTransformation(t, map[string]any{
		"field":       "operation",
		"mapping":     `{"update":"changed"}`,
		"mappingType": "exact",
		"targetField": "kind",
	}, "mapping", sample)

	after, ok := resp["after"].(map[string]any)
	if !ok {
		t.Fatalf("after is %T, want an object (response: %#v)", resp["after"], resp)
	}
	if after["kind"] != "changed" {
		t.Errorf("kind = %#v, want %q -- a transformation could not read the operation (response: %#v)",
			after["kind"], "changed", resp)
	}
}

// A CDC row is free to have a column called "table". It is data, and it must
// survive -- both in the after-image and when a transformation reads it, where
// it outranks the virtual field of the same name.
func TestPreview_ADataColumnNamedTableOutranksTheVirtualField(t *testing.T) {
	sample := map[string]any{
		"operation": "create",
		"table":     "bookings",
		"after":     map[string]any{"table": "corner-booth", "covers": 4},
	}

	resp := postTransformation(t, map[string]any{
		"field":       "table",
		"mapping":     `{"corner-booth":"window"}`,
		"mappingType": "exact",
		"targetField": "moved_to",
	}, "mapping", sample)

	if resp["table"] != "bookings" {
		t.Errorf("root table = %#v, want %q (response: %#v)", resp["table"], "bookings", resp)
	}

	after, ok := resp["after"].(map[string]any)
	if !ok {
		t.Fatalf("after is %T, want an object (response: %#v)", resp["after"], resp)
	}
	if after["table"] != "corner-booth" {
		t.Errorf("after[\"table\"] = %#v, want %q -- the data column was lost or overwritten by the "+
			"message's table name (after: %#v)", after["table"], "corner-booth", after)
	}
	if after["moved_to"] != "window" {
		t.Errorf("moved_to = %#v, want %q -- the transformation read the message's table name instead "+
			"of the data column (after: %#v)", after["moved_to"], "window", after)
	}
}
