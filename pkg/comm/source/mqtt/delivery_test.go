package mqtt

import (
	"testing"
	"time"

	sourcebuf "github.com/user/hermod/pkg/comm/source"
)

// What the broker callback does with a message it cannot hand over yet.
//
// The handler used to drop the *oldest* buffered message to make room for the
// newest, then push. Three things wrong with that, and none of them announced
// themselves:
//
//   - the drop was silent — no error, no log, no metric — so a consumer falling
//     behind lost data while every status stayed green;
//   - the discarded message was taken off the channel and abandoned rather than
//     released, so each drop permanently removed one message from the pool;
//   - the push that followed was a blocking send outside any select, so if the
//     channel refilled in between, the callback parked inside Paho's delivery
//     goroutine and stopped every subsequent message for that client.
//
// A source is not the right place to choose what to discard. The engine already
// has a backpressure policy with metrics behind it (BPDropOldest / BPDropNewest,
// counted by hermod_engine_backpressure_drop_total); a source that quietly drops
// first takes that decision away and hides it. Waiting is the honest behaviour:
// MQTT flow control stops the broker sending, and at QoS 1 and above it
// redelivers.

// fakeMessage lives in sample_test.go; these use it rather than a second stub.

func newTestSource(t *testing.T) *Source {
	t.Helper()
	s, err := NewSource(map[string]string{
		"broker_url": "tcp://127.0.0.1:1883",
		"topic":      "test/topic",
	})
	if err != nil {
		t.Fatalf("NewSource: %v", err)
	}
	return s
}

// A burst larger than the buffer must not lose its oldest messages.
func TestABurstLargerThanTheBufferKeepsEveryMessage(t *testing.T) {
	s := newTestSource(t)
	t.Cleanup(func() { _ = s.Close() })

	handler := s.opts.DefaultPublishHandler
	if handler == nil {
		t.Fatal("no publish handler installed; this test drives the one the broker calls")
	}

	// More than the buffer holds. The sender runs first and alone, so the
	// buffer is genuinely full before anything drains it — which is the state
	// the old drop-oldest branch existed to handle.
	total := sourcebuf.DefaultSourceBuffer + 8
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range total {
			handler(nil, &fakeMessage{topic: "test/topic", payload: []byte{byte(i)}, messageID: uint16(i)})
		}
	}()

	// Let it fill and block. With the fix the sender parks at the buffer limit
	// and finishes once draining starts; without it, it evicts and races ahead.
	time.Sleep(200 * time.Millisecond)

	// Bounded, so a message that never arrives is a failure rather than a hang.
	seen := map[byte]bool{}
	deadline := time.After(10 * time.Second)
drain:
	for len(seen) < total {
		select {
		case msg := <-s.msgCh:
			if msg == nil {
				continue
			}
			if p := msg.Payload(); len(p) == 1 {
				seen[p[0]] = true
			}
			msg.Release()
		case <-deadline:
			break drain
		}
	}
	<-done

	if len(seen) != total {
		t.Errorf("%d of %d messages arrived; %d were dropped by the broker callback\n"+
			"discarding the oldest to make room loses data with no error, no log and no "+
			"metric, and takes a decision away from the engine's backpressure policy, "+
			"which would have counted it",
			len(seen), total, total-len(seen))
	}
}

// Close must not turn a message still being delivered into a panic.
//
// Close used to close msgCh while the broker callback was still sending to it,
// which is "send on closed channel" — a crash of the whole process, not an
// error the engine can recover from. Paho's Disconnect does not guarantee no
// handler is in flight.
func TestAMessageArrivingAfterCloseDoesNotPanic(t *testing.T) {
	s := newTestSource(t)
	handler := s.opts.DefaultPublishHandler

	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("a message delivered after Close panicked: %v\n"+
				"the broker callback sends to a channel Close had already closed, so a "+
				"message in flight during shutdown takes the process down", r)
		}
	}()
	handler(nil, &fakeMessage{topic: "test/topic", payload: []byte("late"), messageID: 1})
}
