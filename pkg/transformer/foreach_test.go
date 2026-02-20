package transformer

import (
	"context"
	"testing"

	"github.com/user/hermod/pkg/message"
)

func TestForeach_Basic(t *testing.T) {
	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	msg.SetData("items", []any{1, 2, 3})

	tf, ok := Get("foreach")
	if !ok {
		t.Fatal("foreach transformer not registered")
	}

	res, err := tf.Transform(context.Background(), msg, map[string]any{
		"arrayPath":   "items",
		"resultField": "_fanout",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil {
		t.Fatal("unexpected nil result")
	}
	data := res.Data()
	v, ok := data["_fanout"].([]any)
	if !ok {
		t.Fatalf("_fanout not an array: %#v", data["_fanout"])
	}
	if len(v) != 3 {
		t.Fatalf("expected 3 items, got %d", len(v))
	}
}

func TestForeach_ItemPath_Index_Limit(t *testing.T) {
	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	msg.SetData("rows", []any{
		map[string]any{"id": 10, "name": "a"},
		map[string]any{"id": 20, "name": "b"},
	})

	tf, _ := Get("foreach")
	res, err := tf.Transform(context.Background(), msg, map[string]any{
		"arrayPath":  "rows",
		"itemPath":   "id",
		"indexField": "_i",
		"limit":      1,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data := res.Data()
	arr, ok := data["_fanout"].([]any)
	if !ok {
		t.Fatalf("_fanout not an array")
	}
	if len(arr) != 1 {
		t.Fatalf("expected limit=1, got %d", len(arr))
	}
}

func TestForeach_DropEmpty(t *testing.T) {
	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	msg.SetData("rows", []any{})

	tf, _ := Get("foreach")
	res, err := tf.Transform(context.Background(), msg, map[string]any{
		"arrayPath": "rows",
		"dropEmpty": true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res != nil {
		t.Fatalf("expected nil (filtered) when dropEmpty, got non-nil")
	}
}
