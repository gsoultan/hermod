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
	sender         gsmail.Sender
	from           string
	to             []string
	subject        string
	formatter      hermod.Formatter
	templateSource string // "inline", "url", "s3"
	template       string
	templateURL    string
	s3Config       gsmail.S3Config
}

// NewSmtpSink creates a new SmtpSink.
func NewSmtpSink(host string, port int, username, password string, ssl bool, from string, to []string, subject string, formatter hermod.Formatter,
	templateSource, template, templateURL string, s3Config gsmail.S3Config) *SmtpSink {
	return &SmtpSink{
		sender:         smtp.NewSender(host, port, username, password, ssl),
		from:           from,
		to:             to,
		subject:        subject,
		formatter:      formatter,
		templateSource: templateSource,
		template:       template,
		templateURL:    templateURL,
		s3Config:       s3Config,
	}
}

// Write sends the message as an email.
func (s *SmtpSink) Write(ctx context.Context, msg hermod.Message) error {
	if msg == nil {
		return nil
	}
	data := msg.Data()
	if data == nil {
		data = make(map[string]interface{})
	}

	email := gsmail.Email{
		From:    s.from,
		To:      s.to,
		Subject: s.subject,
	}

	switch s.templateSource {
	case "url":
		if err := email.SetBodyFromURL(ctx, s.templateURL, data); err != nil {
			return fmt.Errorf("failed to set body from URL: %w", err)
		}
	case "s3":
		if err := email.SetBodyFromS3(ctx, s.s3Config, data); err != nil {
			return fmt.Errorf("failed to set body from S3: %w", err)
		}
	default: // "inline" or empty
		if s.template != "" {
			if err := email.SetBody(s.template, data); err != nil {
				return fmt.Errorf("failed to set body: %w", err)
			}
		} else {
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
			email.Body = body
		}
	}

	return s.sender.Send(ctx, email)
}

// Ping checks the connection to the SMTP server.
func (s *SmtpSink) Ping(ctx context.Context) error {
	return s.sender.Ping(ctx)
}

// Close closes the SMTP sink.
func (s *SmtpSink) Close() error {
	return nil
}
