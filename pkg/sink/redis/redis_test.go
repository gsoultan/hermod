package redis

import (
	"context"
	"testing"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

func TestRedisSink_Write(t *testing.T) {
	snk, err := NewRedisSink("localhost:6379", "", "cdc-stream", nil)
	if err != nil {
		t.Fatalf("failed to create RedisSink: %v", err)
	}
	defer snk.Close()

	msg := message.AcquireMessage()
	msg.SetID("test-1")
	msg.SetOperation(hermod.OpCreate)
	msg.SetTable("users")
	msg.SetSchema("public")
	msg.SetAfter([]byte(`{"id": 1}`))

	err = snk.Write(context.Background(), msg)
	if err != nil {
		t.Errorf("failed to write to RedisSink: %v", err)
	}
}
