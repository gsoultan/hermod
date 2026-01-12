package json

import (
	"encoding/json"
	"testing"

	"github.com/user/hermod/pkg/message"
)

func TestJSONFormatter(t *testing.T) {
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

	var parsed map[string]interface{}
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

	// Verify if 'after' is a map (object) or string (base64)
	after := parsed["after"]
	if _, ok := after.(map[string]interface{}); !ok {
		t.Errorf("expected 'after' to be a map[string]interface{}, got %T", after)
	}
}
