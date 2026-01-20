package smtp

import (
	"context"
	"testing"

	"github.com/gsoultan/gsmail"
	"github.com/user/hermod"
)

type mockSender struct {
	lastEmail  gsmail.Email
	sendCalled bool
}

func (m *mockSender) Send(ctx context.Context, email gsmail.Email) error {
	m.lastEmail = email
	m.sendCalled = true
	return nil
}

func (m *mockSender) Ping(ctx context.Context) error {
	return nil
}

func (m *mockSender) Validate(ctx context.Context, email string) error {
	return nil
}

type mockMessage struct {
	id string
}

func (m *mockMessage) ID() string                  { return m.id }
func (m *mockMessage) Operation() hermod.Operation { return hermod.OpCreate }
func (m *mockMessage) Table() string               { return "test_table" }
func (m *mockMessage) Schema() string              { return "test_schema" }
func (m *mockMessage) Before() []byte              { return nil }
func (m *mockMessage) After() []byte               { return []byte(`{"id":1}`) }
func (m *mockMessage) Payload() []byte             { return nil }
func (m *mockMessage) Metadata() map[string]string { return nil }
func (m *mockMessage) Data() map[string]interface{} {
	return map[string]interface{}{
		"id":   "123",
		"name": "Test User",
	}
}
func (m *mockMessage) SetMetadata(key, value string)         {}
func (m *mockMessage) SetData(key string, value interface{}) {}
func (m *mockMessage) Clone() hermod.Message                 { return m }

func TestSmtpSink_Write_Template(t *testing.T) {
	mock := &mockSender{}
	sink := &SmtpSink{
		sender:         mock,
		from:           "from@example.com",
		to:             []string{"to@example.com"},
		subject:        "Test Subject",
		templateSource: "inline",
		template:       "<h1>Hello {{.name}}</h1>",
	}

	msg := &mockMessage{id: "123"}
	err := sink.Write(context.Background(), msg)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	if !mock.sendCalled {
		t.Error("Send was not called")
	}

	expectedBody := "<h1>Hello Test User</h1>"
	if string(mock.lastEmail.Body) != expectedBody {
		t.Errorf("Expected body %s, got %s", expectedBody, string(mock.lastEmail.Body))
	}
}

func TestSmtpSink_Write_PlainTemplate(t *testing.T) {
	mock := &mockSender{}
	sink := &SmtpSink{
		sender:         mock,
		from:           "from@example.com",
		to:             []string{"to@example.com"},
		subject:        "Test Subject",
		templateSource: "inline",
		template:       "Hello {{.name}}",
	}

	msg := &mockMessage{id: "123"}
	err := sink.Write(context.Background(), msg)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	expectedBody := "Hello Test User"
	if string(mock.lastEmail.Body) != expectedBody {
		t.Errorf("Expected body %s, got %s", expectedBody, string(mock.lastEmail.Body))
	}
}
func (m *mockMessage) ClearPayloads() {}

func TestSmtpSink_Ping(t *testing.T) {
	// For mockSender, Ping should return nil as it's not *smtp.Sender
	mock := &mockSender{}
	sink := &SmtpSink{
		sender: mock,
	}

	err := sink.Ping(context.Background())
	if err != nil {
		t.Errorf("Ping failed for mock: %v", err)
	}
}

func TestSmtpSink_Write(t *testing.T) {
	mock := &mockSender{}
	sink := &SmtpSink{
		sender:  mock,
		from:    "from@example.com",
		to:      []string{"to@example.com"},
		subject: "Test Subject",
	}

	msg := &mockMessage{id: "123"}
	err := sink.Write(context.Background(), msg)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	if !mock.sendCalled {
		t.Error("Send was not called")
	}

	if mock.lastEmail.From != "from@example.com" {
		t.Errorf("Expected from from@example.com, got %s", mock.lastEmail.From)
	}

	if len(mock.lastEmail.To) != 1 || mock.lastEmail.To[0] != "to@example.com" {
		t.Errorf("Expected to to@example.com, got %v", mock.lastEmail.To)
	}

	if mock.lastEmail.Subject != "Test Subject" {
		t.Errorf("Expected subject Test Subject, got %s", mock.lastEmail.Subject)
	}
}
