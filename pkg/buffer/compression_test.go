package buffer

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/compression"
	"github.com/user/hermod/pkg/message"
)

func TestFileBufferWithCompression(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "hermod-buffer-compression-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	algorithms := []compression.Algorithm{compression.LZ4, compression.Snappy, compression.Zstd}

	for _, algo := range algorithms {
		t.Run(string(algo), func(t *testing.T) {
			dir := fmt.Sprintf("%s/%s", tempDir, algo)
			comp, _ := compression.NewCompressor(algo)
			fb, err := NewFileBufferWithCompressor(dir, 100, comp)
			if err != nil {
				t.Fatalf("failed to create FileBuffer: %v", err)
			}
			defer fb.Close()

			ctx := context.Background()

			// Use large payload to trigger compression
			largePayload := make([]byte, 2048)
			for i := range largePayload {
				largePayload[i] = byte(i % 256)
			}

			msg := message.AcquireMessage()
			msg.SetID("msg-1")
			msg.SetPayload(largePayload)

			if err := fb.Produce(ctx, msg); err != nil {
				t.Fatalf("failed to produce message: %v", err)
			}

			// Verify we can read it back correctly
			consumed := false
			consumeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()

			err = fb.Consume(consumeCtx, func(ctx context.Context, m hermod.Message) error {
				if m.ID() != "msg-1" {
					t.Errorf("expected ID msg-1, got %s", m.ID())
				}
				if len(m.Payload()) != len(largePayload) {
					t.Errorf("expected payload length %d, got %d", len(largePayload), len(m.Payload()))
				}
				consumed = true
				cancel()
				return nil
			})

			if err != nil && err != context.Canceled && err != context.DeadlineExceeded {
				t.Fatalf("consume failed: %v", err)
			}

			if !consumed {
				t.Error("message was not consumed")
			}
		})
	}
}
