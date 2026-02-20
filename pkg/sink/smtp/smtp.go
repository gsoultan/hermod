package smtp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/gsoultan/gsmail"
	"github.com/gsoultan/gsmail/smtp"
	"github.com/user/hermod"
)

// SmtpSink implements the hermod.Sink interface for SMTP.
type SmtpSink struct {
	sender            gsmail.Sender
	from              string
	to                []string
	subject           string
	formatter         hermod.Formatter
	templateSource    string // "inline", "url", "s3"
	template          string
	templateURL       string
	s3Config          gsmail.S3Config
	outlookCompatible bool

	lastPing time.Time
	pingMu   sync.Mutex

	// Optional idempotency support
	idemStore         IdempotencyStore
	enableIdempotency bool
	lastWriteDedup    bool
	lastWriteConflict bool
	// Optional key template for idempotency key; when empty we derive from content
	idemKeyTemplate string
}

const smtpPingInterval = 5 * time.Minute

// NewSmtpSink creates a new SmtpSink.
func NewSmtpSink(host string, port int, username, password string, ssl bool, from string, to []string, subject string, formatter hermod.Formatter,
	templateSource, template, templateURL string, s3Config gsmail.S3Config, outlookCompatible bool) *SmtpSink {
	return &SmtpSink{
		sender:            smtp.NewSender(host, port, username, password, ssl),
		from:              from,
		to:                to,
		subject:           subject,
		formatter:         formatter,
		templateSource:    templateSource,
		template:          template,
		templateURL:       templateURL,
		s3Config:          s3Config,
		outlookCompatible: outlookCompatible,
	}
}

// Write sends the message as an email.
func (s *SmtpSink) Write(ctx context.Context, msg hermod.Message) error {
	if msg == nil {
		return nil
	}
	data := msg.Data()

	// Create a copy of the data and add system fields for the template
	templateData := make(map[string]any)
	for k, v := range data {
		templateData[k] = v
	}

	// Ensure system fields are available if not shadowed
	if _, ok := templateData["id"]; !ok {
		templateData["id"] = msg.ID()
	}
	if _, ok := templateData["operation"]; !ok {
		templateData["operation"] = msg.Operation()
	}
	if _, ok := templateData["table"]; !ok {
		templateData["table"] = msg.Table()
	}
	if _, ok := templateData["schema"]; !ok {
		templateData["schema"] = msg.Schema()
	}
	if _, ok := templateData["metadata"]; !ok {
		templateData["metadata"] = msg.Metadata()
	}

	templateData = PrepareTemplateData(templateData)

	// Render dynamic recipients and subject
	to := make([]string, 0, len(s.to))
	for _, recipient := range s.to {
		if strings.Contains(recipient, "{{") {
			rendered, err := renderTemplate(recipient, templateData)
			if err != nil {
				return fmt.Errorf("failed to render recipient template %s: %w", recipient, err)
			}
			// Split by comma in case the template variable contains multiple emails
			parts := strings.Split(rendered, ",")
			for _, p := range parts {
				if trimmed := strings.TrimSpace(p); trimmed != "" {
					to = append(to, trimmed)
				}
			}
		} else {
			to = append(to, recipient)
		}
	}

	// Normalize and de-duplicate recipients (case-insensitive)
	to = normalizeAndDedupeEmails(to)

	subject := s.subject
	if strings.Contains(subject, "{{") {
		rendered, err := renderTemplate(subject, templateData)
		if err != nil {
			return fmt.Errorf("failed to render subject template: %w", err)
		}
		subject = rendered
	}

	email := gsmail.Email{
		From:              s.from,
		To:                to,
		Subject:           subject,
		OutlookCompatible: s.outlookCompatible,
	}

	switch s.templateSource {
	case "url":
		if err := email.SetBodyFromURL(ctx, s.templateURL, templateData); err != nil {
			return fmt.Errorf("failed to set body from URL: %w", err)
		}
	case "s3":
		if err := email.SetBodyFromS3(ctx, s.s3Config, templateData); err != nil {
			return fmt.Errorf("failed to set body from S3: %w", err)
		}
	default: // "inline" or empty
		if s.template != "" {
			if err := email.SetBody(s.template, templateData); err != nil {
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

	// Idempotency: compute a stable key once the email is fully built
	s.lastWriteDedup = false
	s.lastWriteConflict = false
	if s.enableIdempotency && s.idemStore != nil {
		// Prefer explicit template if configured
		var key string
		if s.idemKeyTemplate != "" {
			keyRendered, err := renderTemplate(s.idemKeyTemplate, templateData)
			if err != nil {
				return fmt.Errorf("render idempotency key template: %w", err)
			}
			key = strings.TrimSpace(keyRendered)
			// Fallback: if the rendered key is empty or <no value>, derive from content
			if key == "" || key == "<no value>" {
				key = computeIdempotencyKey(msg, email)
			}
		} else {
			key = computeIdempotencyKey(msg, email)
		}
		claimed, err := s.idemStore.Claim(ctx, key)
		if err != nil {
			return fmt.Errorf("claim idempotency: %w", err)
		}
		if !claimed {
			// Already processed — no-op success
			s.lastWriteDedup = true
			return nil
		}
		// Proceed to send; on success, mark sent
		if err := s.sender.Send(ctx, email); err != nil {
			return err
		}
		if err := s.idemStore.MarkSent(ctx, key); err != nil {
			return fmt.Errorf("mark sent: %w", err)
		}
		return nil
	}

	return s.sender.Send(ctx, email)
}

// PrepareTemplateData enhances the data map for better template usability.
// It unmarshals 'after' and 'before' JSON strings if present, and flattens 'after' fields.
func PrepareTemplateData(data map[string]any) map[string]any {
	// Try to unmarshal 'after' and 'before' if they are JSON strings
	for _, key := range []string{"after", "before"} {
		if val, ok := data[key]; ok {
			var nested map[string]any
			if str, ok := val.(string); ok && strings.HasPrefix(strings.TrimSpace(str), "{") {
				if err := json.Unmarshal([]byte(str), &nested); err == nil {
					data[key] = nested
				}
			} else if m, ok := val.(map[string]any); ok {
				nested = m
			}

			// If we have unmarshaled/nested data, and it's 'after', also flatten it to the root
			// for easier access (if not colliding with existing fields).
			if key == "after" && nested != nil {
				for nk, nv := range nested {
					if _, exists := data[nk]; !exists {
						data[nk] = nv
					}
				}
			}
		}
	}
	return data
}

// Ping checks the connection to the SMTP server.
func (s *SmtpSink) Ping(ctx context.Context) error {
	s.pingMu.Lock()
	defer s.pingMu.Unlock()

	// Rate limit pings to avoid spamming the SMTP server.
	// If the last ping was successful and within the interval, skip.
	if !s.lastPing.IsZero() && time.Since(s.lastPing) < smtpPingInterval {
		return nil
	}

	err := s.sender.Ping(ctx)
	if err == nil {
		s.lastPing = time.Now()
	}
	return err
}

// Close closes the SMTP sink.
func (s *SmtpSink) Close() error {
	return nil
}

func renderTemplate(tmplStr string, data any) (string, error) {
	tmpl, err := template.New("smtp").Parse(tmplStr)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// IdempotencyStore is a minimal interface to guard against duplicate sends.
type IdempotencyStore interface {
	// Claim returns true if this call created the record (we own it),
	// false if it already exists.
	Claim(ctx context.Context, key string) (bool, error)
	// MarkSent records a successful completion for the key.
	MarkSent(ctx context.Context, key string) error
}

// computeIdempotencyKey creates a deterministic key from message id and email content.
func computeIdempotencyKey(msg hermod.Message, email gsmail.Email) string {
	h := fnv.New128a()
	// Stable inputs
	_, _ = h.Write([]byte(msg.ID()))
	_, _ = h.Write([]byte("|"))
	_, _ = h.Write([]byte(strings.ToLower(email.Subject)))
	_, _ = h.Write([]byte("|"))
	for _, r := range email.To {
		_, _ = h.Write([]byte(strings.ToLower(strings.TrimSpace(r))))
		_, _ = h.Write([]byte(","))
	}
	_, _ = h.Write([]byte("|"))
	_, _ = h.Write(email.Body)
	sum := h.Sum(nil)
	// Encode as hex without importing extra packages
	const hexdigits = "0123456789abcdef"
	out := make([]byte, len(sum)*2)
	for i, b := range sum {
		out[i*2] = hexdigits[b>>4]
		out[i*2+1] = hexdigits[b&0x0f]
	}
	return string(out)
}

// normalizeAndDedupeEmails lowercases, trims, removes empties and duplicates preserving order.
func normalizeAndDedupeEmails(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, e := range in {
		n := strings.ToLower(strings.TrimSpace(e))
		if n == "" {
			continue
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	return out
}

// Implement hermod.IdempotencyReporter
func (s *SmtpSink) LastWriteIdempotent() (bool, bool) {
	return s.lastWriteDedup, s.lastWriteConflict
}

// EnableIdempotency toggles duplicate protection.
func (s *SmtpSink) EnableIdempotency(v bool) { s.enableIdempotency = v }

// SetIdempotencyStore injects the store implementation.
func (s *SmtpSink) SetIdempotencyStore(store IdempotencyStore) { s.idemStore = store }

// SetIdempotencyKeyTemplate sets an optional template to compute the idempotency key.
func (s *SmtpSink) SetIdempotencyKeyTemplate(tmpl string) {
	s.idemKeyTemplate = strings.TrimSpace(tmpl)
}
