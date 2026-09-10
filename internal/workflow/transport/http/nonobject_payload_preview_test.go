package http

import (
	"testing"

	_ "github.com/gsoultan/hermod/pkg/comm/transformer/core"
)

// The editor's live preview posts the source sample back to
// POST /api/transformations/test. When a source delivers plain strings rather
// than JSON objects, that sample carries the body under "payload" -- so this
// asserts the whole round trip the panel depends on: the body survives the
// preview, and a transformation can actually address it.
//
// Before non-object payloads were decoded, the sample arrived body-less and
// there was nothing here to map.
func TestPreview_NonObjectPayload_IsPreservedAndAddressable(t *testing.T) {
	sample := map[string]any{
		"id":      "m1",
		"payload": "hello world",
		"metadata": map[string]any{
			"delivery_tag": "1",
		},
	}

	resp := postTransformation(t, map[string]any{
		"field":       "payload",
		"mapping":     `{"hello world":"greeting"}`,
		"mappingType": "exact",
		"targetField": "kind",
	}, "mapping", sample)

	// The body itself must still be in the previewed result, otherwise the
	// panel shows an empty message for a queue that plainly has data on it.
	if got := previewedField(t, resp, "payload"); got != "hello world" {
		t.Errorf("payload = %#v; want %q (response: %#v)", got, "hello world", resp)
	}

	// And it must be reachable as a field, or no transformation can act on it.
	if got := previewedField(t, resp, "kind"); got != "greeting" {
		t.Errorf("kind = %#v; want %q -- the mapping could not read payload (response: %#v)",
			got, "greeting", resp)
	}
}
