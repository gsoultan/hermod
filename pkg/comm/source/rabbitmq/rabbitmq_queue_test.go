package rabbitmq

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	jsonfmt "github.com/gsoultan/hermod/pkg/comm/formatter/json"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

func TestRabbitMQQueueSource_SampleFromLastConsumed(t *testing.T) {
	tests := []struct {
		name        string
		stored      bool
		body        []byte
		wantErr     bool
		wantPayload string
		wantField   string
		wantValue   any
	}{
		{
			name:    "no message consumed yet",
			stored:  false,
			wantErr: true,
		},
		{
			name:        "json payload decoded into data",
			stored:      true,
			body:        []byte(`{"test":"data"}`),
			wantPayload: `{"test":"data"}`,
			wantField:   "test",
			wantValue:   "data",
		},
		{
			// A queue carrying plain strings rather than JSON objects still has
			// to surface a field the workflow editor can map, otherwise the
			// body is invisible to every transformation and sink.
			name:        "non-json payload exposed under the payload field",
			stored:      true,
			body:        []byte("plain text"),
			wantPayload: "plain text",
			wantField:   message.NonObjectPayloadKey,
			wantValue:   "plain text",
		},
		{
			name:        "json array exposed under the payload field",
			stored:      true,
			body:        []byte(`["a","b"]`),
			wantPayload: `["a","b"]`,
			wantField:   message.NonObjectPayloadKey,
			wantValue:   []any{"a", "b"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			url := "amqp://guest:guest@localhost:5672/"
			queue := "test_last_consumed_" + tc.name
			src, _ := NewRabbitMQQueueSource(url, queue)

			if tc.stored {
				storeLastConsumed(url, queue, tc.body)
			}

			msg, err := src.sampleFromLastConsumed()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("sampleFromLastConsumed failed: %v", err)
			}
			if got := string(msg.Payload()); got != tc.wantPayload {
				t.Errorf("payload = %q; want %q", got, tc.wantPayload)
			}
			if tc.wantField != "" {
				if got := msg.Data()[tc.wantField]; !reflect.DeepEqual(got, tc.wantValue) {
					t.Errorf("data[%q] = %#v; want %#v", tc.wantField, got, tc.wantValue)
				}
			}
		})
	}
}

// TestRabbitMQQueueSource_Sample exercises the Sample path the source wizard
// calls to show a user what their queue contains.
//
// It was skipped unconditionally -- `t.Skip("...needs live RabbitMQ")` with no
// condition attached -- so it never ran even when a broker was available, and
// the nightly job has been starting RabbitMQ for it all along. That is the same
// shape as the MongoDB lease filter and the MySQL CDC fields: a test that could
// not fail, guarding code nobody was checking.
//
// It now gates on RABBITMQ_URL like every other integration test here, so it
// skips when there is no broker and runs when there is.
func TestRabbitMQQueueSource_Sample(t *testing.T) {
	if os.Getenv("HERMOD_INTEGRATION") != "1" {
		t.Skip("integration: set HERMOD_INTEGRATION=1 to run")
	}
	url := os.Getenv("RABBITMQ_URL")
	if url == "" {
		t.Skip("integration: set RABBITMQ_URL to run")
	}
	queue := "test_sample_queue"

	// Setup: publish a message
	conn, err := amqp.Dial(url)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close()

	ch, err := conn.Channel()
	if err != nil {
		t.Fatalf("failed to open channel: %v", err)
	}
	defer ch.Close()

	_, err = ch.QueueDeclare(queue, true, false, false, false, nil)
	if err != nil {
		t.Fatalf("failed to declare queue: %v", err)
	}

	payload := `{"test":"data"}`
	err = ch.Publish("", queue, false, false, amqp.Publishing{
		ContentType: "application/json",
		Body:        []byte(payload),
	})
	if err != nil {
		t.Fatalf("failed to publish: %v", err)
	}

	// Test Sample
	src, _ := NewRabbitMQQueueSource(url, queue)
	msg, err := src.Sample(t.Context(), "")
	if err != nil {
		t.Fatalf("Sample failed: %v", err)
	}

	if string(msg.Payload()) != payload {
		t.Errorf("expected payload %s, got %s", payload, string(msg.Payload()))
	}

	// Verify message still exists in queue (by trying to get it again)
	d, ok, err := ch.Get(queue, true)
	if err != nil || !ok {
		t.Errorf("message should still be in queue")
	}
	if string(d.Body) != payload {
		t.Errorf("message body mismatch in second get")
	}
}

// TestRabbitMQQueueSource_NonObjectPayload_ReachesSink drives the real consumer
// loop against a live broker.
//
// The unit tests above cover buildSampleMessage, which the wizard calls. This
// one covers run() -> Read() -> Formatter, which is what a running workflow
// actually executes, and asserts the end that used to lose data: a queue
// carrying plain strings rather than JSON objects, feeding a sink configured
// with format: json. That combination silently published a body-less
// {"id":...,"metadata":{...}} before the payload decode was fixed.
func TestRabbitMQQueueSource_NonObjectPayload_ReachesSink(t *testing.T) {
	if os.Getenv("HERMOD_INTEGRATION") != "1" {
		t.Skip("integration: set HERMOD_INTEGRATION=1 to run")
	}
	url := os.Getenv("RABBITMQ_URL")
	if url == "" {
		t.Skip("integration: set RABBITMQ_URL to run")
	}

	cases := []struct {
		name      string
		body      string
		wantValue any
	}{
		{"plain string", "hello world", "hello world"},
		{"csv line", "id,name,qty", "id,name,qty"},
		{"bare number", "42", float64(42)},
		{"json array", `["a","b"]`, []any{"a", "b"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			queue := "hermod_test_nonobject_" + strings.ReplaceAll(tc.name, " ", "_")

			conn, err := amqp.Dial(url)
			if err != nil {
				t.Fatalf("dial: %v", err)
			}
			defer conn.Close()
			ch, err := conn.Channel()
			if err != nil {
				t.Fatalf("channel: %v", err)
			}
			defer ch.Close()

			// durable=true to match the source's own declaration; a mismatch
			// makes the broker reject the second declare with PRECONDITION_FAILED.
			if _, err := ch.QueueDeclare(queue, true, false, false, false, nil); err != nil {
				t.Fatalf("declare: %v", err)
			}
			// Leave nothing behind on a shared broker.
			defer func() { _, _ = ch.QueueDelete(queue, false, false, false) }()
			if _, err := ch.QueuePurge(queue, false); err != nil {
				t.Fatalf("purge: %v", err)
			}

			if err := ch.Publish("", queue, false, false, amqp.Publishing{
				ContentType: "text/plain",
				Body:        []byte(tc.body),
			}); err != nil {
				t.Fatalf("publish: %v", err)
			}

			src, err := NewRabbitMQQueueSource(url, queue)
			if err != nil {
				t.Fatalf("new source: %v", err)
			}
			defer src.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			msg, err := src.Read(ctx)
			if err != nil {
				t.Fatalf("read: %v", err)
			}

			if got := string(msg.Payload()); got != tc.body {
				t.Errorf("Payload() = %q; want %q", got, tc.body)
			}
			if got := msg.Data()[message.NonObjectPayloadKey]; !reflect.DeepEqual(got, tc.wantValue) {
				t.Errorf("Data()[%q] = %#v; want %#v",
					message.NonObjectPayloadKey, got, tc.wantValue)
			}

			// The sink side: format: json is the path that used to drop the body.
			out, err := jsonfmt.NewJSONFormatter().Format(msg)
			if err != nil {
				t.Fatalf("format: %v", err)
			}
			var decoded map[string]any
			if err := json.Unmarshal(out, &decoded); err != nil {
				t.Fatalf("formatter emitted invalid JSON %s: %v", out, err)
			}
			if got := decoded[message.NonObjectPayloadKey]; !reflect.DeepEqual(got, tc.wantValue) {
				t.Errorf("body missing from sink output %s: %q = %#v, want %#v",
					out, message.NonObjectPayloadKey, got, tc.wantValue)
			}

			if err := src.Ack(ctx, msg); err != nil {
				t.Errorf("ack: %v", err)
			}
		})
	}
}
