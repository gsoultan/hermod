package buffer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

func TestFileBuffer(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "hermod-buffer-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	fb, err := NewFileBuffer(tempDir, 10)
	if err != nil {
		t.Fatalf("failed to create FileBuffer: %v", err)
	}
	defer fb.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Produce some messages
	for i := 0; i < 5; i++ {
		msg := message.AcquireMessage()
		msg.SetID(fmt.Sprintf("msg-%d", i))
		msg.SetTable("test_table")
		if err := fb.Produce(ctx, msg); err != nil {
			t.Errorf("failed to produce message %d: %v", i, err)
		}
	}

	// Consume messages
	consumedCount := 0
	consumeCtx, consumeCancel := context.WithCancel(ctx)

	err = fb.Consume(consumeCtx, func(ctx context.Context, msg hermod.Message) error {
		if dm, ok := msg.(*message.DefaultMessage); ok {
			defer message.ReleaseMessage(dm)
		}
		expectedID := fmt.Sprintf("msg-%d", consumedCount)
		if msg.ID() != expectedID {
			t.Errorf("expected ID %s, got %s", expectedID, msg.ID())
		}
		consumedCount++
		if consumedCount == 5 {
			consumeCancel()
		}
		return nil
	})

	if err != nil && !errors.Is(err, context.Canceled) {
		t.Errorf("consume returned error: %v", err)
	}

	if consumedCount != 5 {
		t.Errorf("expected 5 messages consumed, got %d", consumedCount)
	}
}

func TestFileBuffer_Persistence(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "hermod-buffer-persistence-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// 1. Produce messages and close
	{
		fb, err := NewFileBuffer(tempDir, 10)
		if err != nil {
			t.Fatalf("failed to create FileBuffer: %v", err)
		}

		msg := message.AcquireMessage()
		msg.SetID("persisted-msg")
		if err := fb.Produce(context.Background(), msg); err != nil {
			t.Fatalf("failed to produce message: %v", err)
		}
		fb.Close()
	}

	// 2. Re-open and consume
	{
		fb, err := NewFileBuffer(tempDir, 10)
		if err != nil {
			t.Fatalf("failed to create FileBuffer: %v", err)
		}
		defer fb.Close()

		consumed := false
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		err = fb.Consume(ctx, func(ctx context.Context, msg hermod.Message) error {
			if dm, ok := msg.(*message.DefaultMessage); ok {
				defer message.ReleaseMessage(dm)
			}
			if msg.ID() != "persisted-msg" {
				t.Errorf("expected ID persisted-msg, got %s", msg.ID())
			}
			consumed = true
			cancel() // Stop consuming
			return nil
		})

		if err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("consume returned error: %v", err)
		}

		if !consumed {
			t.Error("message was not consumed after restart")
		}
	}
}
