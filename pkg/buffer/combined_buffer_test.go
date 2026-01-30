package buffer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

func TestCombinedBuffer_ProduceConsume(t *testing.T) {
	dir := filepath.Join(os.TempDir(), "hermod-buffer-test")
	_ = os.RemoveAll(dir)
	cb, err := NewCombinedBuffer(8, dir, 0, &CombinedOptions{ProduceTimeout: 1 * time.Millisecond})
	if err != nil {
		t.Fatalf("new combined buffer: %v", err)
	}
	defer cb.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Produce more than ring capacity to force spill to file
	const N = 32
	for i := 0; i < N; i++ {
		m := message.AcquireMessage()
		m.SetID(fmt.Sprintf("%d", i))
		if err := cb.Produce(ctx, m); err != nil {
			t.Fatalf("produce %d: %v", i, err)
		}
	}

	consumed := 0
	go func() {
		_ = cb.Consume(ctx, func(_ context.Context, _m hermod.Message) error {
			consumed++
			return nil
		})
	}()

	// Wait for consumption
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if consumed >= N {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if consumed < N {
		t.Fatalf("expected to consume %d, got %d", N, consumed)
	}
}
