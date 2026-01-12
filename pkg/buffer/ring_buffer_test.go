package buffer

import (
	"context"
	"errors"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
	"testing"
	"time"
)

func TestRingBuffer(t *testing.T) {
	rb := NewRingBuffer(10)
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	msg := message.AcquireMessage()
	msg.SetID("1")
	msg.SetPayload([]byte("hello"))

	go func() {
		err := rb.Produce(ctx, msg)
		if err != nil {
			t.Errorf("failed to produce: %v", err)
		}
	}()

	handler := func(ctx context.Context, m hermod.Message) error {
		if m.ID() != "1" {
			t.Errorf("expected ID 1, got %s", m.ID())
		}
		if string(m.Payload()) != "hello" {
			t.Errorf("expected payload hello, got %s", string(m.Payload()))
		}
		cancel() // Stop consuming after receiving the message
		return nil
	}

	err := rb.Consume(ctx, handler)
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Errorf("consume failed: %v", err)
	}

	rb.Close()
	message.ReleaseMessage(msg)
}
