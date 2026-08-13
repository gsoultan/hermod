//go:build integration

package rabbitmq

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/comm/message"
)

// ---------------------------------------------------------------------------
// RabbitMQ queue sink, against a real broker.
//
// The source has a live test and is GA; the sink is Beta on the strength of
// unit tests alone. The conformance suite proves it refuses a nil message and
// closes cleanly, which is not the same as proving a published message is
// retrievable from the queue afterwards — and publishing is the one thing it
// exists to do.
//
// Run with:
//
//	HERMOD_INTEGRATION=1 RABBITMQ_URL=amqp://guest:guest@127.0.0.1:5672/ \
//	go test -tags=integration ./pkg/comm/sink/rabbitmq/
// ---------------------------------------------------------------------------

type rabbitSinkFixture struct {
	url   string
	queue string
	conn  *amqp.Connection
	ch    *amqp.Channel
	sink  *RabbitMQQueueSink
}

func newRabbitSinkFixture(t *testing.T) *rabbitSinkFixture {
	t.Helper()

	url := os.Getenv("RABBITMQ_URL")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || url == "" {
		t.Skip("integration: set HERMOD_INTEGRATION=1 and RABBITMQ_URL to run")
	}

	// A failure, not a skip. RABBITMQ_URL being set says a broker should be
	// there; skipping when it is not turns a broken environment into a green run
	// that tested nothing.
	conn, err := amqp.Dial(url)
	if err != nil {
		t.Fatalf("RABBITMQ_URL names a broker that is not reachable (%s): %v", url, err)
	}
	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		t.Fatalf("rabbitmq channel on %s: %v", url, err)
	}

	queue := fmt.Sprintf("hermod_sink_%x", time.Now().UnixNano())
	sink, err := NewRabbitMQQueueSink(url, queue, nil)
	if err != nil {
		t.Fatalf("new sink: %v", err)
	}

	f := &rabbitSinkFixture{url: url, queue: queue, conn: conn, ch: ch, sink: sink}
	t.Cleanup(func() {
		_ = sink.Close()
		// A leaked queue is not tidiness: an unrouted durable queue keeps every
		// message published to it until someone notices the disk.
		_, _ = ch.QueueDelete(queue, false, false, false)
		_ = ch.Close()
		_ = conn.Close()
	})
	return f
}

// consume returns up to want messages, waiting no longer than timeout.
func (f *rabbitSinkFixture) consume(t *testing.T, want int, timeout time.Duration) []string {
	t.Helper()
	deliveries, err := f.ch.Consume(f.queue, "", true, false, false, false, nil)
	if err != nil {
		t.Fatalf("consume: %v", err)
	}

	var got []string
	deadline := time.After(timeout)
	for len(got) < want {
		select {
		case d, ok := <-deliveries:
			if !ok {
				return got
			}
			got = append(got, string(d.Body))
		case <-deadline:
			return got
		}
	}
	return got
}

func rabbitMessage(t *testing.T, id, name string) hermod.Message {
	t.Helper()
	m := message.AcquireMessage()
	m.SetID(id)
	m.SetOperation(hermod.OpCreate)
	m.SetTable("people")
	body, _ := json.Marshal(map[string]any{"id": id, "name": name})
	m.SetAfter(body)
	m.SetData("id", id)
	m.SetData("name", name)
	t.Cleanup(func() { message.ReleaseMessage(m) })
	return m
}

// TestAPublishedMessageIsRetrievable is the evidence GA asks for: not that the
// sink accepted the write, but that the message is in the queue afterwards.
func TestAPublishedMessageIsRetrievable(t *testing.T) {
	f := newRabbitSinkFixture(t)

	if err := f.sink.Write(t.Context(), rabbitMessage(t, "r-1", "Ada")); err != nil {
		t.Fatalf("write: %v", err)
	}

	got := f.consume(t, 1, 10*time.Second)
	if len(got) != 1 {
		t.Fatalf("published one message and consumed %d; the write was accepted and "+
			"nothing reached the queue", len(got))
	}
	if !strings.Contains(got[0], "Ada") {
		t.Errorf("the body does not carry the record: %s", got[0])
	}
}

// TestEveryMessageInARunArrives. This sink publishes one at a time, so the
// risk is not a dropped batch tail but a connection that fails partway and is
// reported as success for the rest.
func TestEveryMessageInARunArrives(t *testing.T) {
	f := newRabbitSinkFixture(t)

	for i := range 20 {
		msg := rabbitMessage(t, fmt.Sprintf("b-%d", i), fmt.Sprintf("name-%d", i))
		if err := f.sink.Write(t.Context(), msg); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}

	got := f.consume(t, 20, 20*time.Second)
	if len(got) != 20 {
		t.Errorf("published 20 messages and consumed %d; the rest were accepted and lost", len(got))
	}
}

// TestWritingAfterCloseFails rather than silently discarding. A sink that
// accepts writes after shutdown loses everything published during a drain.
func TestWritingAfterCloseFails(t *testing.T) {
	f := newRabbitSinkFixture(t)

	if err := f.sink.Write(t.Context(), rabbitMessage(t, "r-2", "Grace")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := f.sink.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Either an error, or a reconnect that genuinely delivers. What must not
	// happen is silent acceptance with nothing published.
	err := f.sink.Write(t.Context(), rabbitMessage(t, "r-3", "Alan"))
	if err != nil {
		return
	}
	got := f.consume(t, 2, 10*time.Second)
	if len(got) < 2 {
		t.Errorf("a write after Close reported success but only %d message(s) reached "+
			"the queue; messages published during a drain would be lost silently", len(got))
	}
}
