package graphql

import (
	"context"
	"testing"

	"github.com/user/hermod"
)

func TestGraphQLSourceRead(t *testing.T) {
	path := "/api/graphql/test"
	src := NewGraphQLSource(path)
	defer src.Close()

	msg := &mockMessage{id: "test-id"}

	go func() {
		_ = Dispatch(path, msg)
	}()

	readMsg, err := src.Read(context.Background())
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	if readMsg.ID() != "test-id" {
		t.Errorf("Expected ID test-id, got %s", readMsg.ID())
	}
}

type mockMessage struct {
	hermod.Message
	id string
}

func (m *mockMessage) ID() string { return m.id }
