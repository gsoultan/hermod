package smtp

import (
	"strings"
	"testing"
)

// BuildEmail is the seam the editor's preview renders through. It has to be
// the same renderer Write uses, or a preview agrees with nothing.
func TestBuildEmail_RendersEverythingWriteWould(t *testing.T) {
	mock := &mockSender{}
	sink := &SmtpSink{
		sender:         mock,
		from:           "ops@example.com",
		to:             []string{"{{ .email }}"},
		subject:        `Order {{ .id }} on {{ .created_at.Format "02 Jan 2006" }}`,
		templateSource: "inline",
		template:       `<p>Hi {{ .name }}</p>`,
	}
	msg := &dataMessage{data: map[string]any{
		"id":         "42",
		"email":      "buyer@example.com",
		"name":       "Ana",
		"created_at": "2026-12-01T09:30:00Z",
	}}

	email, err := sink.BuildEmail(t.Context(), msg)
	if err != nil {
		t.Fatalf("BuildEmail failed: %v", err)
	}
	if want := "Order 42 on 01 Dec 2026"; email.Subject != want {
		t.Errorf("subject = %q, want %q", email.Subject, want)
	}
	if len(email.To) != 1 || email.To[0] != "buyer@example.com" {
		t.Errorf("recipients = %v, want [buyer@example.com]", email.To)
	}
	if want := "<p>Hi Ana</p>"; string(email.Body) != want {
		t.Errorf("body = %q, want %q", string(email.Body), want)
	}
	if mock.sendCalled {
		t.Error("BuildEmail sent the email; it must only render")
	}
}

func TestBuildEmail_ReportsABrokenTemplate(t *testing.T) {
	sink := &SmtpSink{
		sender:         &mockSender{},
		from:           "ops@example.com",
		to:             []string{"to@example.com"},
		subject:        "Subject",
		templateSource: "inline",
		template:       `{{ .created_at.Format }}`,
	}
	_, err := sink.BuildEmail(t.Context(), &dataMessage{data: map[string]any{"created_at": "2026-12-01"}})
	if err == nil {
		t.Fatal("a template calling Format with no layout was accepted")
	}
	if !strings.Contains(err.Error(), "Format") {
		t.Errorf("error %q does not point at the template", err)
	}
}

func TestBuildEmail_RefusesANilMessage(t *testing.T) {
	sink := &SmtpSink{sender: &mockSender{}, to: []string{"to@example.com"}}
	if _, err := sink.BuildEmail(t.Context(), nil); err == nil {
		t.Fatal("a nil message rendered an email")
	}
}
