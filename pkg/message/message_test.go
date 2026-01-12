package message

import (
	"github.com/user/hermod"
	"testing"
)

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

	expected := `{"id":"test-id","operation":"update","table":"consumer","schema":"dbo","before":{"id":1,"name":"John Doe"},"after":{"id":1,"name":"Johnathan Doe"},"metadata":{"source":"mssql"}}`
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
	expected = `{"id":"test-id-2","operation":"","table":"","schema":""}`
	if string(data) != expected {
		t.Errorf("expected %s, got %s", expected, string(data))
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
			after:     []byte("after"),
			payload:   []byte("payload"),
			metadata:  map[string]string{"key": "value"},
		}
		_ = m.Metadata()
	}
}
