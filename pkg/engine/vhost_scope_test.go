package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/buffer"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// The engine marks every message it reads with its workflow's vhost. That mark
// is what secret() answers for, further down the pipeline and in a sink's own
// templates; a source cannot supply it, because it is not part of what a
// source delivers.
func TestEngineMarksWhatItReadsWithItsVHost(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	msg := message.AcquireMessage()
	msg.SetID("test-1")
	msg.SetPayload([]byte("hello"))
	// What the source delivered claims another vhost, in both places a payload
	// can write.
	msg.SetData("vhost", "tenant-b")
	msg.SetMetadata("vhost", "tenant-b")

	source := &mockSource{msg: msg}
	sink := &mockSink{received: make(chan hermod.Message, 1)}
	eng := NewEngine(source, []hermod.Sink{sink}, buffer.NewRingBuffer(10))
	eng.SetVHost("tenant-a")

	go func() {
		err := eng.Start(ctx)
		if err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
			t.Errorf("engine error: %v", err)
		}
	}()

	select {
	case received := <-sink.received:
		scoped, ok := received.(hermod.VHostScoped)
		if !ok {
			t.Fatal("the delivered message is not VHostScoped")
		}
		if got := scoped.VHost(); got != "tenant-a" {
			t.Errorf("the delivered message's vhost = %q, want tenant-a", got)
		}
	case <-time.After(time.Second):
		t.Error("timeout waiting for message")
	}
}
