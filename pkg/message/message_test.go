package message

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/user/hermod"
)

func TestDefaultMessage_Payload(t *testing.T) {
	msg := AcquireMessage()
	defer ReleaseMessage(msg)

	// 1. Explicit payload
	msg.SetPayload([]byte("explicit"))
	if string(msg.Payload()) != "explicit" {
		t.Errorf("expected explicit, got %s", string(msg.Payload()))
	}

	// 2. Fallback to After
	msg.ClearPayloads()
	msg.SetAfter([]byte("after"))
	if string(msg.Payload()) != "after" {
		t.Errorf("expected after, got %s", string(msg.Payload()))
	}

	// 3. Fallback to Data
	msg.ClearPayloads()
	msg.SetData("foo", "bar")
	if string(msg.Payload()) != `{"foo":"bar"}` {
		t.Errorf("expected {\"foo\":\"bar\"}, got %s", string(msg.Payload()))
	}
}

func TestAcquireRelease(t *testing.T) {
	msg := AcquireMessage()
	if msg == nil {
		t.Fatal("AcquireMessage returned nil")
	}

	msg.SetID("test-id")
	msg.SetOperation(hermod.OpCreate)
	msg.SetTable("users")
	msg.SetSchema("public")
	msg.SetBefore([]byte("before"))
	msg.SetAfter([]byte("after"))
	msg.SetPayload([]byte("test-payload"))
	msg.SetMetadata("key", "value")

	ReleaseMessage(msg)

	msg2 := AcquireMessage()
	if msg2.ID() != "" {
		t.Errorf("expected empty ID after release/acquire, got %s", msg2.ID())
	}
	if msg2.Operation() != "" {
		t.Errorf("expected empty operation, got %s", msg2.Operation())
	}
	if msg2.Table() != "" {
		t.Errorf("expected empty table, got %s", msg2.Table())
	}
	if msg2.Schema() != "" {
		t.Errorf("expected empty schema, got %s", msg2.Schema())
	}
	if len(msg2.Before()) != 0 {
		t.Errorf("expected empty before, got %v", msg2.Before())
	}
	if len(msg2.After()) != 0 {
		t.Errorf("expected empty after, got %v", msg2.After())
	}
	if len(msg2.Payload()) != 0 {
		t.Errorf("expected empty payload after release/acquire, got %v", msg2.Payload())
	}
	if len(msg2.Metadata()) != 0 {
		t.Errorf("expected empty metadata after release/acquire, got %v", msg2.Metadata())
	}

	// Ensure it implements the interface
	var _ hermod.Message = (*DefaultMessage)(nil)
}

func TestDefaultMessage_MarshalJSON(t *testing.T) {
	msg := AcquireMessage()
	defer ReleaseMessage(msg)

	msg.SetID("test-id")
	msg.SetOperation(hermod.OpUpdate)
	msg.SetTable("consumer")
	msg.SetSchema("dbo")
	msg.SetBefore([]byte(`{"id":1,"name":"John Doe"}`))
	msg.SetAfter([]byte(`{"id":1,"name":"Johnathan Doe"}`))
	msg.SetMetadata("source", "mssql")

	data, err := msg.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON failed: %v", err)
	}

	// map keys are sorted alphabetically when marshaled
	expected := `{"before":{"id":1,"name":"John Doe"},"id":"test-id","metadata":{"source":"mssql"},"name":"Johnathan Doe","operation":"update","schema":"dbo","table":"consumer"}`
	if string(data) != expected {
		t.Errorf("expected %s, got %s", expected, string(data))
	}

	// Test omitempty
	msg.Reset()
	msg.SetID("test-id-2")
	data, err = msg.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON failed: %v", err)
	}
	expected = `{"id":"test-id-2"}`
	if string(data) != expected {
		t.Errorf("expected %s, got %s", expected, string(data))
	}

	// Test dynamic data
	msg.Reset()
	msg.SetID("dyn-id")
	msg.SetData("foo", "bar")
	msg.SetData("num", 123)
	data, err = msg.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON failed: %v", err)
	}
	expected = `{"foo":"bar","id":"dyn-id","num":123}`
	if string(data) != expected {
		t.Errorf("expected %s, got %s", expected, string(data))
	}
}

func TestSanitizeValue(t *testing.T) {
	id := uuid.New()

	tests := []struct {
		name     string
		input    interface{}
		expected interface{}
	}{
		{
			name:     "string",
			input:    "test",
			expected: "test",
		},
		{
			name:     "int",
			input:    123,
			expected: 123,
		},
		{
			name:     "uuid.UUID",
			input:    id,
			expected: id.String(),
		},
		{
			name:     "byte slice (uuid)",
			input:    id[:],
			expected: id.String(),
		},
		{
			name:     "byte array (uuid)",
			input:    [16]byte(id),
			expected: id.String(),
		},
		{
			name:     "pointer to uuid",
			input:    &id,
			expected: id.String(),
		},
		{
			name:     "nil",
			input:    nil,
			expected: nil,
		},
		{
			name:     "byte slice (not uuid)",
			input:    []byte{1, 2, 3},
			expected: []byte{1, 2, 3},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeValue(tt.input)
			if tt.name == "byte slice (not uuid)" {
				// Special case because slice comparison is tricky
				if string(got.([]byte)) != string(tt.expected.([]byte)) {
					t.Errorf("SanitizeValue() = %v, want %v", got, tt.expected)
				}
				return
			}
			if got != tt.expected {
				t.Errorf("SanitizeValue() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestSanitizeMap(t *testing.T) {
	id1 := uuid.New()
	id2 := uuid.New()

	input := map[string]interface{}{
		"id":    id1,
		"raw":   id2[:],
		"name":  "test",
		"count": 10,
	}

	sanitized := SanitizeMap(input)

	if sanitized["id"] != id1.String() {
		t.Errorf("id not sanitized: got %v, want %v", sanitized["id"], id1.String())
	}
	if sanitized["raw"] != id2.String() {
		t.Errorf("raw not sanitized: got %v, want %v", sanitized["raw"], id2.String())
	}
	if sanitized["name"] != "test" {
		t.Errorf("name changed: got %v, want %v", sanitized["name"], "test")
	}
}

func TestMessageSetDataSanitization(t *testing.T) {
	id := uuid.New()
	msg := AcquireMessage()
	defer ReleaseMessage(msg)

	msg.SetData("uuid", id)
	msg.SetData("bytes", id[:])

	data := msg.Data()
	if data["uuid"] != id.String() {
		t.Errorf("SetData didn't sanitize uuid: got %v, want %v", data["uuid"], id.String())
	}
	if data["bytes"] != id.String() {
		t.Errorf("SetData didn't sanitize bytes: got %v, want %v", data["bytes"], id.String())
	}
}

func TestMessageMarshalJSONSanitization(t *testing.T) {
	id := uuid.New()
	msg := AcquireMessage()
	defer ReleaseMessage(msg)

	msg.SetData("user_id", [16]byte(id))

	bz, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("failed to marshal message: %v", err)
	}

	var res map[string]interface{}
	if err := json.Unmarshal(bz, &res); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}

	if res["user_id"] != id.String() {
		t.Errorf("JSON output not sanitized: got %v, want %v", res["user_id"], id.String())
	}
}

func BenchmarkAcquireRelease(b *testing.B) {
	for i := 0; i < b.N; i++ {
		msg := AcquireMessage()
		msg.SetID("id")
		msg.SetPayload([]byte("payload"))
		ReleaseMessage(msg)
	}
}

func BenchmarkNoPool(b *testing.B) {
	for i := 0; i < b.N; i++ {
		m := &DefaultMessage{
			id:        "id",
			operation: hermod.OpCreate,
			table:     "users",
			schema:    "public",
			before:    []byte("before"),
			payload:   []byte("payload"),
			metadata:  map[string]string{"key": "value"},
		}
		_ = m.Metadata()
	}
}
