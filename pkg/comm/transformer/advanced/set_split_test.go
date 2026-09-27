package advanced_test

import (
	"testing"

	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/comm/transformer"
	_ "github.com/gsoultan/hermod/pkg/comm/transformer/advanced"
)

// A set node reaches split the way the running system does: the stored config
// is `column.<path>` keys holding expressions, the registry hands the
// transformer out by name, and Prepare parses the columns before Transform
// evaluates them. Splitting a name into two fields used to take two nodes -- a
// Data Conversion to make a list, then a set node to index it.
func TestSetNodeSplitsATextFieldIntoFields(t *testing.T) {
	tr, ok := transformer.Get("set")
	if !ok {
		t.Fatal("no transformer is registered as set")
	}
	cfg := map[string]any{
		"transType":         "set",
		"column.first_name": "split(source.full_name, ' ', 0)",
		"column.last_name":  "split(source.full_name, ' ', -1)",
	}
	if p, ok := tr.(transformer.PreparedTransformer); ok {
		var err error
		if cfg, err = p.Prepare(cfg); err != nil {
			t.Fatalf("Prepare: %v", err)
		}
	}

	msg := message.AcquireMessage()
	msg.SetData("full_name", "Ada King Lovelace")
	out, err := tr.Transform(t.Context(), msg, cfg)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}

	got := out.Data()
	if got["first_name"] != "Ada" || got["last_name"] != "Lovelace" {
		t.Errorf("first_name = %#v, last_name = %#v; want \"Ada\" and \"Lovelace\"",
			got["first_name"], got["last_name"])
	}
}
