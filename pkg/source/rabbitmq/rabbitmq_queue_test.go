package rabbitmq

import (
	"context"
	"testing"

	amqp "github.com/rabbitmq/amqp091-go"
)

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
	msg, err := src.Sample(context.Background(), "")
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
