package transformer

import (
	"context"
	"testing"

	"github.com/user/hermod/pkg/message"
	_ "modernc.org/sqlite"
)

func TestNewObjectConcept(t *testing.T) {
	// A CDC message from source
	msg := message.AcquireMessage()
	msg.SetTable("users")
	msg.SetOperation("update")
	msg.SetBefore([]byte(`{"id": 1, "name": "old"}`))
	msg.SetAfter([]byte(`{"id": 1, "name": "new", "email": "new@example.com"}`))
	msg.SetMetadata("source", "mysql")

	// Transformation that creates a "new object"
	// We only want the email, and we want to rename it to 'contact'
	tr := &MappingTransformer{
		Mapping: map[string]string{
			"email": "contact",
		},
		Strict: true,
	}

	res, err := tr.Transform(context.Background(), msg)
	if err != nil {
		t.Fatalf("Transform failed: %v", err)
	}

	// Verify the result
	data := getMessageData(res)
	if data["contact"] != "new@example.com" {
		t.Errorf("Expected contact=new@example.com, got %v", data["contact"])
	}
	if _, ok := data["name"]; ok {
		t.Errorf("Field 'name' should have been removed in strict mode")
	}

	// Check remnants in the message object
	if len(res.Before()) > 0 {
		t.Errorf("'Before' data still exists: %s", string(res.Before()))
	}
}

func TestMergeObjectConcept(t *testing.T) {
	// A CDC message from source
	msg := message.AcquireMessage()
	msg.SetTable("users")
	msg.SetOperation("update")
	msg.SetBefore([]byte(`{"id": 1, "name": "old"}`))
	msg.SetAfter([]byte(`{"id": 1, "name": "new", "email": "new@example.com"}`))

	// Transformation that MERGES data
	tr := &MappingTransformer{
		Mapping: map[string]string{
			"email": "contact",
		},
		Strict: false,
	}

	res, err := tr.Transform(context.Background(), msg)
	if err != nil {
		t.Fatalf("Transform failed: %v", err)
	}

	// Verify the result
	data := getMessageData(res)
	if data["contact"] != "new@example.com" {
		t.Errorf("Expected contact=new@example.com, got %v", data["contact"])
	}
	if data["name"] != "new" {
		t.Errorf("Field 'name' should have been preserved in non-strict mode")
	}

	// Check remnants in the message object - Before should STILL EXIST
	if len(res.Before()) == 0 {
		t.Errorf("'Before' data should have been preserved in non-strict mode")
	}
}

func TestSqlTransformerNewObject(t *testing.T) {
	// SqlTransformer should always create a new object
	tr := &SqlTransformer{
		Driver: "sqlite",
		Conn:   ":memory:",
		Query:  "SELECT 'transformed' as status, :id as original_id",
	}

	msg := message.AcquireMessage()
	msg.SetID("123")
	msg.SetBefore([]byte(`{"id": "123", "val": "old"}`))
	msg.SetAfter([]byte(`{"id": "123", "val": "new"}`))

	res, err := tr.Transform(context.Background(), msg)
	if err != nil {
		t.Fatalf("Transform failed: %v", err)
	}

	data := getMessageData(res)
	if data["status"] != "transformed" {
		t.Errorf("Expected status=transformed, got %v", data["status"])
	}

	if len(res.Before()) > 0 {
		t.Errorf("SqlTransformer should have cleared Before data")
	}
}
