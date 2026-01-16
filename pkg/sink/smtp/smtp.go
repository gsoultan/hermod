package smtp

import (
	"context"
	"fmt"

	"github.com/gsoultan/gsmail"
	"github.com/gsoultan/gsmail/smtp"
	"github.com/user/hermod"
)

// SmtpSink implements the hermod.Sink interface for SMTP.
type SmtpSink struct {
	sender    gsmail.Sender
	from      string
	to        []string
	subject   string
	formatter hermod.Formatter
}

// NewSmtpSink creates a new SmtpSink.
func NewSmtpSink(host string, port int, username, password string, ssl bool, from string, to []string, subject string, formatter hermod.Formatter) *SmtpSink {
	return &SmtpSink{
		sender:    smtp.NewSender(host, port, username, password, ssl),
		from:      from,
		to:        to,
		subject:   subject,
		formatter: formatter,
	}
}

// Write sends the message as an email.
func (s *SmtpSink) Write(ctx context.Context, msg hermod.Message) error {
	var body []byte
	var err error

	if s.formatter != nil {
		body, err = s.formatter.Format(msg)
	} else {
		body = msg.Payload()
	}

	if err != nil {
		return fmt.Errorf("failed to format message: %w", err)
	}

	email := gsmail.Email{
		From:    s.from,
		To:      s.to,
		Subject: s.subject,
		Body:    body,
	}

	return s.sender.Send(ctx, email)
}

// Ping checks the connection to the SMTP server.
func (s *SmtpSink) Ping(ctx context.Context) error {
	return nil
}

// Close closes the SMTP sink.
func (s *SmtpSink) Close() error {
	return nil
}
