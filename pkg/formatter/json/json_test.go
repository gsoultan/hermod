package json

import (
	"encoding/json"
	"testing"

	"github.com/user/hermod/pkg/message"
)

func TestJSONFormatter(t *testing.T) {
	t.Run("Full Mode", func(t *testing.T) {
		formatter := NewJSONFormatter()

		msg := message.AcquireMessage()
		msg.SetID("test-id")
		msg.SetTable("users")
		msg.SetSchema("public")
		msg.SetAfter([]byte(`{"name":"john"}`))

		data, err := formatter.Format(msg)
		if err != nil {
			t.Fatalf("failed to format message: %v", err)
		}

		var parsed map[string]any
		err = json.Unmarshal(data, &parsed)
		if err != nil {
			t.Fatalf("failed to unmarshal JSON: %v", err)
		}

		if parsed["id"] != "test-id" {
			t.Errorf("expected id test-id, got %v", parsed["id"])
		}
		if parsed["table"] != "users" {
			t.Errorf("expected table users, got %v", parsed["table"])
		}

		// Verify that data fields are merged into root
		if parsed["name"] != "john" {
			t.Errorf("expected name john at root, got %v", parsed["name"])
		}

		// Verify that 'after' key is NOT present (unified format)
		if _, ok := parsed["after"]; ok {
			t.Error("expected 'after' key to be absent in unified format")
		}
	})

	t.Run("Payload Mode", func(t *testing.T) {
		formatter := NewJSONFormatter()
		formatter.SetMode(ModePayload)

		msg := message.AcquireMessage()
		msg.SetAfter([]byte(`{"name":"john"}`))

		data, err := formatter.Format(msg)
		if err != nil {
			t.Fatalf("failed to format message: %v", err)
		}

		if string(data) != `{"name":"john"}` {
			t.Errorf("expected payload, got %s", string(data))
		}
	})
}
