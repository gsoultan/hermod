package smtp

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"strings"

	"github.com/gsoultan/gsmail"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
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

func (m *mockSender) SetRetryConfig(config gsmail.RetryConfig) {}

type pingCounterSender struct {
	mockSender
	pingCount int
}

func (m *pingCounterSender) Ping(ctx context.Context) error {
	m.pingCount++
	return nil
}

// mockIdemStore is a simple in-memory implementation to test idempotency behavior.
type mockIdemStore struct {
	seen map[string]struct{}
}

func (s *mockIdemStore) Claim(ctx context.Context, key string) (bool, error) {
	if s.seen == nil {
		s.seen = make(map[string]struct{})
	}
	if _, ok := s.seen[key]; ok {
		return false, nil
	}
	s.seen[key] = struct{}{}
	return true, nil
}

func (s *mockIdemStore) MarkSent(ctx context.Context, key string) error { return nil }

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
func (m *mockMessage) Data() map[string]any {
	return map[string]any{
		"id":   "123",
		"name": "Test User",
	}
}
func (m *mockMessage) SetMetadata(key, value string) {}
func (m *mockMessage) SetData(key string, value any) {}
func (m *mockMessage) Clone() hermod.Message         { return m }
func (m *mockMessage) ClearPayloads()                {}

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

func TestSmtpSink_Write_DynamicRecipientsAndSubject(t *testing.T) {
	mock := &mockSender{}
	sink := &SmtpSink{
		sender:         mock,
		from:           "from@example.com",
		to:             []string{"static@example.com", "{{.recipient_email}}"},
		subject:        "Alert for {{.table}}",
		templateSource: "inline",
		template:       "Hello {{.name}}",
	}

	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	msg.SetData("recipient_email", "dynamic@example.com")
	msg.SetData("name", "John Doe")
	msg.SetData("table", "users")

	err := sink.Write(context.Background(), msg)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	expectedTo := []string{"static@example.com", "dynamic@example.com"}
	if len(mock.lastEmail.To) != len(expectedTo) {
		t.Errorf("Expected %d recipients, got %d", len(expectedTo), len(mock.lastEmail.To))
	}
	for i, email := range mock.lastEmail.To {
		if email != expectedTo[i] {
			t.Errorf("Expected recipient %d to be %s, got %s", i, expectedTo[i], email)
		}
	}

	expectedSubject := "Alert for users"
	if mock.lastEmail.Subject != expectedSubject {
		t.Errorf("Expected subject %s, got %s", expectedSubject, mock.lastEmail.Subject)
	}
}

func TestSmtpSink_NormalizeAndDedupeRecipients(t *testing.T) {
	mock := &mockSender{}
	sink := &SmtpSink{
		sender:         mock,
		from:           "from@example.com",
		to:             []string{"Alice@Example.com ", " alice@example.com", "Bob@example.com", "bob@example.com", "  "},
		subject:        "x",
		template:       "ok",
		templateSource: "inline",
	}
	msg := &mockMessage{id: "123"}
	if err := sink.Write(context.Background(), msg); err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	got := mock.lastEmail.To
	want := []string{"alice@example.com", "bob@example.com"}
	if len(got) != len(want) {
		t.Fatalf("expected %d recipients, got %d: %v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("recipient %d mismatch: want %q got %q", i, want[i], got[i])
		}
	}
}
func TestSmtpSink_ArrayParameter(t *testing.T) {
	mock := &mockSender{}
	sink := &SmtpSink{
		sender:  mock,
		from:    "sender@example.com",
		to:      []string{"recipient@example.com"},
		subject: "Table Test",
		template: `
<table>
  {{range .items}}
  <tr><td>{{.name}}</td><td>{{.price}}</td></tr>
  {{end}}
</table>`,
	}

	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	msg.SetData("items", []map[string]any{
		{"name": "Item 1", "price": 10.5},
		{"name": "Item 2", "price": 20.0},
	})

	err := sink.Write(context.Background(), msg)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	body := string(mock.lastEmail.Body)
	if !strings.Contains(body, "<tr><td>Item 1</td><td>10.5</td>") {
		t.Errorf("Expected body to contain Item 1, but got:\n%s", body)
	}
	if !strings.Contains(body, "<tr><td>Item 2</td><td>20</td>") {
		t.Errorf("Expected body to contain Item 2, but got:\n%s", body)
	}
}

func TestSmtpSink_PayloadArray(t *testing.T) {
	mock := &mockSender{}
	sink := &SmtpSink{
		sender:   mock,
		from:     "sender@example.com",
		to:       []string{"recipient@example.com"},
		subject:  "Payload Array Test",
		template: `{{range .payload}}{{.}}{{end}}`,
	}

	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	msg.SetPayload([]byte(`["a", "b", "c"]`))

	err := sink.Write(context.Background(), msg)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	body := string(mock.lastEmail.Body)
	if body != "abc" {
		t.Errorf("Expected body abc, but got %s", body)
	}
}

func TestSmtpSink_SystemFieldsInTemplate(t *testing.T) {
	mock := &mockSender{}
	sink := &SmtpSink{
		sender:   mock,
		to:       []string{"to@example.com"},
		template: `ID: {{.id}}, Table: {{.table}}, Op: {{.operation}}`,
	}

	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	msg.SetID("msg-1")
	msg.SetTable("users")
	msg.SetOperation(hermod.OpUpdate)

	err := sink.Write(context.Background(), msg)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	body := string(mock.lastEmail.Body)
	expected := "ID: msg-1, Table: users, Op: update"
	if body != expected {
		t.Errorf("Expected %q, got %q", expected, body)
	}
}

func TestSmtpSink_IfElseTemplate(t *testing.T) {
	mock := &mockSender{}
	sink := &SmtpSink{
		sender:   mock,
		to:       []string{"to@example.com"},
		template: `{{if eq .operation "create"}}New record added: {{.id}}{{else}}Record {{.id}} updated{{end}}`,
	}

	// Test 'create' branch
	msg := message.AcquireMessage()
	msg.SetID("101")
	msg.SetOperation(hermod.OpCreate)
	err := sink.Write(context.Background(), msg)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if string(mock.lastEmail.Body) != "New record added: 101" {
		t.Errorf("Expected 'New record added: 101', got %q", string(mock.lastEmail.Body))
	}
	message.ReleaseMessage(msg)

	// Test 'else' branch
	msg = message.AcquireMessage()
	msg.SetID("101")
	msg.SetOperation(hermod.OpUpdate)
	err = sink.Write(context.Background(), msg)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if string(mock.lastEmail.Body) != "Record 101 updated" {
		t.Errorf("Expected 'Record 101 updated', got %q", string(mock.lastEmail.Body))
	}
	message.ReleaseMessage(msg)
}

func TestSmtpSink_OutlookCompatible(t *testing.T) {
	mock := &mockSender{}
	sink := &SmtpSink{
		sender:            mock,
		to:                []string{"to@example.com"},
		outlookCompatible: true,
	}

	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)

	err := sink.Write(context.Background(), msg)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	if !mock.lastEmail.OutlookCompatible {
		t.Error("Expected OutlookCompatible to be true in gsmail.Email")
	}

	sink.outlookCompatible = false
	err = sink.Write(context.Background(), msg)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	if mock.lastEmail.OutlookCompatible {
		t.Error("Expected OutlookCompatible to be false in gsmail.Email")
	}
}

func TestSmtpSink_JSONStringFields(t *testing.T) {
	mock := &mockSender{}
	sink := &SmtpSink{
		sender:   mock,
		to:       []string{"to@example.com"},
		template: `ID: {{.after.id}}, Name: {{.after.name}}, FlatName: {{.name}}`,
	}

	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)

	samplePayload := `{
  "after": "{\"id\":2,\"name\":\"test-user\"}",
  "id": "system-id"
}`
	msg.SetPayload([]byte(samplePayload))

	err := sink.Write(context.Background(), msg)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	body := string(mock.lastEmail.Body)
	// .after.id and .after.name should be reachable because 'after' was unmarshaled.
	// .name should be reachable because 'after' fields were flattened.
	// .id should be 'system-id' because system fields are not overwritten by flattening.
	expected := "ID: 2, Name: test-user, FlatName: test-user"
	if !strings.Contains(body, expected) {
		t.Errorf("Expected body to contain %q, but got %q", expected, body)
	}
	if !strings.Contains(body, "ID: 2") {
		t.Errorf("Expected after.id to be 2, but got %q", body)
	}
}

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

func TestSmtpSink_Ping_RateLimit(t *testing.T) {
	mock := &pingCounterSender{}
	sink := &SmtpSink{
		sender: mock,
	}

	// First ping should go through
	err := sink.Ping(context.Background())
	if err != nil {
		t.Fatalf("First ping failed: %v", err)
	}
	if mock.pingCount != 1 {
		t.Errorf("Expected 1 ping, got %d", mock.pingCount)
	}

	// Second ping immediately after should be rate limited
	err = sink.Ping(context.Background())
	if err != nil {
		t.Fatalf("Second ping failed: %v", err)
	}
	if mock.pingCount != 1 {
		t.Errorf("Expected still 1 ping, got %d", mock.pingCount)
	}

	// Manually reset lastPing to simulate time passage
	sink.lastPing = time.Now().Add(-6 * time.Minute)
	err = sink.Ping(context.Background())
	if err != nil {
		t.Fatalf("Third ping failed: %v", err)
	}
	if mock.pingCount != 2 {
		t.Errorf("Expected 2 pings, got %d", mock.pingCount)
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

func TestSmtpSink_IdempotencySkipDuplicate(t *testing.T) {
	mock := &mockSender{}
	idem := &mockIdemStore{}
	sink := &SmtpSink{
		sender:            mock,
		from:              "from@example.com",
		to:                []string{"to@example.com"},
		subject:           "Sub {{.id}}",
		templateSource:    "inline",
		template:          "Hello {{.id}}",
		outlookCompatible: false,
		idemStore:         idem,
		enableIdempotency: true,
	}

	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	msg.SetID("dup-1")
	// first write sends
	if err := sink.Write(context.Background(), msg); err != nil {
		t.Fatalf("first write failed: %v", err)
	}
	if !mock.sendCalled {
		t.Fatalf("expected first send to be called")
	}
	// reset flag and attempt duplicate
	mock.sendCalled = false
	if err := sink.Write(context.Background(), msg); err != nil {
		t.Fatalf("second write failed: %v", err)
	}
	if mock.sendCalled {
		t.Fatalf("expected duplicate to be skipped (no send)")
	}
	if dedup, _ := sink.LastWriteIdempotent(); !dedup {
		t.Fatalf("expected last write to be marked idempotent")
	}
}

func TestSmtpSink_IdempotencyEmptyKeyFallback(t *testing.T) {
	t.Run("template renders empty", func(t *testing.T) {
		mock := &mockSender{}
		idem := &mockIdemStore{}
		sink := &SmtpSink{
			sender:            mock,
			from:              "from@example.com",
			to:                []string{"to@example.com"},
			subject:           "Sub {{.id}}",
			templateSource:    "inline",
			template:          "Hello {{.id}}",
			outlookCompatible: false,
			idemStore:         idem,
			enableIdempotency: true,
		}
		// Key template points to a missing field -> renders empty
		sink.SetIdempotencyKeyTemplate("{{.missing}}")

		// msg1 should send once, and duplicate should be skipped
		msg1 := message.AcquireMessage()
		defer message.ReleaseMessage(msg1)
		msg1.SetID("id-1")
		if err := sink.Write(context.Background(), msg1); err != nil {
			t.Fatalf("first write msg1 failed: %v", err)
		}
		if !mock.sendCalled {
			t.Fatalf("expected first send to be called for msg1")
		}
		mock.sendCalled = false
		if err := sink.Write(context.Background(), msg1); err != nil {
			t.Fatalf("second write msg1 failed: %v", err)
		}
		if mock.sendCalled {
			t.Fatalf("expected duplicate msg1 to be skipped (no send)")
		}

		// msg2 has different ID -> different derived key -> should send
		msg2 := message.AcquireMessage()
		defer message.ReleaseMessage(msg2)
		msg2.SetID("id-2")
		mock.sendCalled = false
		if err := sink.Write(context.Background(), msg2); err != nil {
			t.Fatalf("first write msg2 failed: %v", err)
		}
		if !mock.sendCalled {
			t.Fatalf("expected first send to be called for msg2")
		}
	})

	t.Run("template blank/whitespace only", func(t *testing.T) {
		mock := &mockSender{}
		idem := &mockIdemStore{}
		sink := &SmtpSink{
			sender:            mock,
			from:              "from@example.com",
			to:                []string{"to@example.com"},
			subject:           "Fixed Subject",
			templateSource:    "inline",
			template:          "Body",
			outlookCompatible: false,
			idemStore:         idem,
			enableIdempotency: true,
		}
		// Set whitespace-only template
		sink.SetIdempotencyKeyTemplate("   \t  ")

		msg1 := message.AcquireMessage()
		defer message.ReleaseMessage(msg1)
		msg1.SetID("a")
		if err := sink.Write(context.Background(), msg1); err != nil {
			t.Fatalf("first write msg1 failed: %v", err)
		}
		mock.sendCalled = false
		// duplicate
		if err := sink.Write(context.Background(), msg1); err != nil {
			t.Fatalf("second write msg1 failed: %v", err)
		}
		if mock.sendCalled {
			t.Fatalf("expected duplicate msg1 to be skipped (no send)")
		}

		// different message id => should send
		msg2 := message.AcquireMessage()
		defer message.ReleaseMessage(msg2)
		msg2.SetID("b")
		mock.sendCalled = false
		if err := sink.Write(context.Background(), msg2); err != nil {
			t.Fatalf("first write msg2 failed: %v", err)
		}
		if !mock.sendCalled {
			t.Fatalf("expected first send for msg2")
		}
	})
}

func TestSmtpSink_RealSend(t *testing.T) {
	host := os.Getenv("SMTP_HOST")
	if host == "" {
		t.Skip("SMTP_HOST not set, skipping real send test")
	}

	port, _ := strconv.Atoi(os.Getenv("SMTP_PORT"))
	user := os.Getenv("SMTP_USER")
	pass := os.Getenv("SMTP_PASS")
	from := os.Getenv("SMTP_FROM")
	to := os.Getenv("SMTP_TO")
	ssl := os.Getenv("SMTP_SSL") == "true"

	sink := NewSmtpSink(host, port, user, pass, ssl, from, []string{to}, "Hermod Test Email", nil, "inline", "<h1>Test Email from Hermod</h1><p>This is a test email sent from Hermod unit tests.</p>", "", gsmail.S3Config{}, true)

	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	msg.SetID("test-real-send")
	msg.SetTable("test_table")
	msg.SetOperation(hermod.OpSnapshot)

	err := sink.Write(context.Background(), msg)
	if err != nil {
		t.Fatalf("Failed to send real email: %v", err)
	}
}
