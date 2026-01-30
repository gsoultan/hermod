package smtp

import (
	"context"
	"strings"
	"testing"

	"github.com/user/hermod/pkg/message"
)

func TestSmtpSink_TrailingEndRepro(t *testing.T) {
	mock := &mockSender{}
	sink := &SmtpSink{
		sender:            mock,
		from:              "from@example.com",
		to:                []string{"to@example.com", "{{.dynamic_to}}"},
		subject:           "Subject {{.table}}",
		template:          "<div>Hello {{.name}}</div>",
		outlookCompatible: true,
	}

	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	msg.SetData("name", "John")
	msg.SetData("dynamic_to", "dynamic@example.com")
	msg.SetTable("users")

	err := sink.Write(context.Background(), msg)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	body := string(mock.lastEmail.Body)
	if strings.Contains(body, "<end>") {
		t.Errorf("Body contains <end>: %q", body)
	}

	subject := mock.lastEmail.Subject
	if strings.Contains(subject, "<end>") {
		t.Errorf("Subject contains <end>: %q", subject)
	}

	for _, to := range mock.lastEmail.To {
		if strings.Contains(to, "<end>") {
			t.Errorf("Recipient contains <end>: %q", to)
		}
	}
}
