package sse

import (
	"context"
	"testing"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/sse"
	"github.com/user/hermod/pkg/message"
)

func TestSSESink_PublishAndReceive(t *testing.T) {
	stream := "test-stream"
	sink := NewSSESink(stream, nil)

	// Subscribe first to avoid race
	ch, unsub := sse.GetHub().Subscribe(stream, 1)
	defer unsub()

	dm := message.AcquireMessage()
	dm.SetID("id-1")
	dm.SetOperation(hermod.OpCreate)
	dm.SetData("hello", "world")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := sink.Write(ctx, dm); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	select {
	case ev := <-ch:
		if string(ev.Data) == "" {
			t.Fatalf("expected data, got empty")
		}
		if ev.ID != "id-1" {
			t.Fatalf("unexpected id: %s", ev.ID)
		}
	case <-ctx.Done():
		t.Fatalf("timeout waiting for event")
	}
}
