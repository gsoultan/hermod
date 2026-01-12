package mysql

import (
	"context"
	"testing"
	"time"

	"github.com/user/hermod"
)

func TestMySQLSource_Read(t *testing.T) {
	s := NewMySQLSource("root:password@tcp(localhost:3306)/inventory")
	defer s.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	msg, err := s.Read(ctx)
	if err != nil {
		t.Fatalf("failed to read from MySQLSource: %v", err)
	}

	if msg.ID() != "mysql-cdc-1" {
		t.Errorf("expected ID mysql-cdc-1, got %s", msg.ID())
	}

	if msg.Operation() != hermod.OpCreate {
		t.Errorf("expected operation create, got %s", msg.Operation())
	}
	if msg.Table() != "products" {
		t.Errorf("expected table products, got %s", msg.Table())
	}
	if msg.Schema() != "inventory" {
		t.Errorf("expected schema inventory, got %s", msg.Schema())
	}
	if string(msg.After()) != `{"id": 50, "name": "Gadget", "price": 19.99}` {
		t.Errorf("unexpected after: %s", string(msg.After()))
	}
	if msg.Metadata()["source"] != "mysql" {
		t.Errorf("expected source mysql, got %s", msg.Metadata()["source"])
	}
}
