//go:build integration
// +build integration

package redis

import (
	"context"
	"os"
	"testing"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

func TestRedisSink_Write(t *testing.T) {
	if os.Getenv("HERMOD_INTEGRATION") != "1" {
		t.Skip("skipping integration test; set HERMOD_INTEGRATION=1 to run")
	}
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		t.Skip("integration test: set REDIS_ADDR to run")
	}

	password := os.Getenv("REDIS_PASSWORD")
	stream := os.Getenv("REDIS_STREAM")
	if stream == "" {
		stream = "cdc-stream"
	}

	snk, err := NewRedisSink(addr, password, stream, nil)
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
