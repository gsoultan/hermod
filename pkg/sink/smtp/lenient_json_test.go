package smtp

import (
	"context"
	"testing"

	"github.com/user/hermod/pkg/message"
)

func TestSmtpSink_LenientJSON(t *testing.T) {
	mockSender := &mockSender{}
	sink := &SmtpSink{
		sender:  mockSender,
		from:    "test@example.com",
		to:      []string{"{{.email}}"},
		subject: "Test Subject",
	}

	// Case 1: Valid JSON from RabbitMQ (non-CDC style)
	// RabbitMQQueueSource calls SetData for each key.
	payload := `{
	  "email": "gembit.shirazi@impack-pratama.com",
	  "isActive": true,
	  "name": "Alice Smith"
	}`

	msg := message.AcquireMessage()
	// Simulate RabbitMQQueueSource behavior
	msg.SetPayload([]byte(payload))
	// In RabbitMQQueueSource:
	// var jsonData map[string]any
	// json.Unmarshal(d.Body, &jsonData)
	// for k, v := range jsonData { hmsg.SetData(k, v) }
	msg.SetData("email", "gembit.shirazi@impack-pratama.com")
	msg.SetData("isActive", true)
	msg.SetData("name", "Alice Smith")

	ctx := context.Background()
	err := sink.Write(ctx, msg)
	if err != nil {
		t.Errorf("Case 1 (Valid JSON) failed: %v", err)
	}

	if !mockSender.sendCalled {
		t.Error("Case 1: Expected email to be sent")
	} else if mockSender.lastEmail.To[0] != "gembit.shirazi@impack-pratama.com" {
		t.Errorf("Case 1: Expected recipient gembit.shirazi@impack-pratama.com, got %s", mockSender.lastEmail.To[0])
	}
	mockSender.sendCalled = false

	// Case 2: Invalid JSON (trailing comma)
	// RabbitMQQueueSource falls back to SetAfter
	payload2 := `{
	  "email": "gembit.shirazi@impack-pratama.com",
	  "isActive": true,
	  "name": "Alice Smith",
	}`
	msg2 := message.AcquireMessage()
	msg2.SetPayload([]byte(payload2))
	msg2.SetAfter([]byte(payload2)) // Fallback behavior

	err = sink.Write(ctx, msg2)
	// This should now succeed thanks to the lenient JSON unmarshaling
	if err != nil {
		t.Errorf("Case 2 (Invalid JSON) failed: %v", err)
	} else if !mockSender.sendCalled {
		t.Error("Case 2: Expected email to be sent")
	} else if mockSender.lastEmail.To[0] != "gembit.shirazi@impack-pratama.com" {
		t.Errorf("Case 2: Expected recipient gembit.shirazi@impack-pratama.com, got %s", mockSender.lastEmail.To[0])
	}
}
