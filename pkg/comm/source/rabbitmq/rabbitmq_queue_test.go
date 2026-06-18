package rabbitmq

import (
	"testing"

	amqp "github.com/rabbitmq/amqp091-go"
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
			name:        "non-json payload preserved as payload",
			stored:      true,
			body:        []byte("plain text"),
			wantPayload: "plain text",
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
				if got := msg.Data()[tc.wantField]; got != tc.wantValue {
					t.Errorf("data[%q] = %v; want %v", tc.wantField, got, tc.wantValue)
				}
			}
		})
	}
}

func TestRabbitMQQueueSource_Sample(t *testing.T) {
	// Skip if no RabbitMQ is available
	t.Skip("Skipping RabbitMQ integration test; needs live RabbitMQ")

	url := "amqp://guest:guest@localhost:5672/"
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
