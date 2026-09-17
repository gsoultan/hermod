package smtp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"maps"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/gsoultan/gsmail"
	gsmailSmtp "github.com/gsoultan/gsmail/smtp"
	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
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
		sender:            gsmailSmtp.NewSender(host, port, username, password, ssl),
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

	email, templateData, err := s.buildEmail(ctx, msg)
	if err != nil {
		return err
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
		// The claim is held across the send and given up if it fails.
		//
		// Without the release, a failed send left the key claimed for good: the
		// retry was told it had already been handled, returned success as a
		// suppressed duplicate, and the message was gone. Any transient SMTP
		// error — a timeout, a refused connection, a rate limit — permanently
		// dropped the message while the pipeline recorded a delivery.
		if err := s.sender.Send(ctx, email); err != nil {
			if rerr := s.idemStore.Release(ctx, key); rerr != nil {
				return fmt.Errorf("send failed (%w) and the idempotency claim could not be "+
					"released (%v), so a retry of this message will be suppressed", err, rerr)
			}
			return err
		}
		if err := s.idemStore.MarkSent(ctx, key); err != nil {
			return fmt.Errorf("mark sent: %w", err)
		}
		return nil
	}

	return s.sender.Send(ctx, email)
}

// BuildEmail renders the sink's templates over a message and returns the email
// it would send, without sending it.
//
// Write goes through it, and so does the editor's template preview. That is the
// whole point of it being one function: a preview rendered by a second, simpler
// renderer agrees with the send right up until the day it matters.
func (s *SmtpSink) BuildEmail(ctx context.Context, msg hermod.Message) (gsmail.Email, error) {
	email, _, err := s.buildEmail(ctx, msg)
	return email, err
}

// buildEmail also hands back the data it rendered over, which the idempotency
// key template needs and a caller that only wants the email does not.
func (s *SmtpSink) buildEmail(ctx context.Context, msg hermod.Message) (gsmail.Email, map[string]any, error) {
	if msg == nil {
		return gsmail.Email{}, nil, errors.New("no message to render")
	}
	templateData := templateDataFrom(msg)

	to, err := s.renderRecipients(templateData)
	if err != nil {
		return gsmail.Email{}, nil, err
	}

	subject := s.subject
	if strings.Contains(subject, "{{") {
		rendered, err := renderTemplate(subject, templateData)
		if err != nil {
			return gsmail.Email{}, nil, fmt.Errorf("failed to render subject template: %w", err)
		}
		subject = rendered
	}

	email := gsmail.Email{
		From:              s.from,
		To:                to,
		Subject:           subject,
		OutlookCompatible: s.outlookCompatible,
	}
	if err := s.setBody(ctx, &email, msg, templateData); err != nil {
		return gsmail.Email{}, nil, err
	}
	return email, templateData, nil
}

// templateDataFrom assembles what a template sees: the row, and then the parts
// of the envelope the row did not already claim.
func templateDataFrom(msg hermod.Message) map[string]any {
	// Create a copy of the data and add system fields for the template
	templateData := make(map[string]any)
	maps.Copy(templateData, msg.Data())

	// If no structured data provided, try to unmarshal payload JSON for convenience
	if payload := msg.Payload(); len(templateData) == 0 && len(payload) > 0 {
		mergePayload(templateData, payload)
	}
	addRowImages(templateData, msg)
	addSystemFields(templateData, msg)

	templateData = PrepareTemplateData(templateData)
	// Recognise the row's dates and times, so that a template can format one
	// where it uses it: {{ .created_at.Format "2006-01-02" }}.
	return withTimestamps(templateData)
}

// mergePayload exposes a JSON payload's fields when the message carries no
// structured data of its own.
func mergePayload(dst map[string]any, payload []byte) {
	var payloadMap map[string]any
	if err := json.Unmarshal(payload, &payloadMap); err == nil {
		maps.Copy(dst, payloadMap)
		return
	}
	// Try once more with a more lenient approach (handling trailing commas)
	if cleaned := message.TryFixJSON(payload); cleaned != nil {
		if err := json.Unmarshal(cleaned, &payloadMap); err == nil {
			maps.Copy(dst, payloadMap)
		}
	}
}

// addRowImages exposes the before and after images, decoded when they are JSON
// and verbatim when they are not.
func addRowImages(dst map[string]any, msg hermod.Message) {
	// If 'after'/'before' fields are not present in data, try to expose them from message payloads
	if _, ok := dst["after"]; !ok && len(msg.After()) > 0 {
		// Try to parse as JSON, else keep as raw string
		var afterMap map[string]any
		if err := json.Unmarshal(msg.After(), &afterMap); err == nil {
			dst["after"] = afterMap
		} else {
			dst["after"] = string(msg.After())
		}
	}
	if _, ok := dst["before"]; !ok && len(msg.Before()) > 0 {
		var beforeMap map[string]any
		if err := json.Unmarshal(msg.Before(), &beforeMap); err == nil {
			dst["before"] = beforeMap
		} else {
			dst["before"] = string(msg.Before())
		}
	}
}

// addSystemFields adds the envelope where the row has not shadowed the name.
func addSystemFields(dst map[string]any, msg hermod.Message) {
	// Ensure system fields are available if not shadowed
	if _, ok := dst["id"]; !ok {
		dst["id"] = msg.ID()
	}
	if _, ok := dst["operation"]; !ok {
		dst["operation"] = msg.Operation()
	}
	if _, ok := dst["table"]; !ok {
		dst["table"] = msg.Table()
	}
	if _, ok := dst["schema"]; !ok {
		dst["schema"] = msg.Schema()
	}
	if _, ok := dst["metadata"]; !ok {
		dst["metadata"] = msg.Metadata()
	}
}

// renderRecipients resolves the configured recipients over the message. A
// template that resolves to nothing is dropped rather than addressed, and a
// send with no one left to address is refused.
func (s *SmtpSink) renderRecipients(templateData map[string]any) ([]string, error) {
	to := make([]string, 0, len(s.to))
	for _, recipient := range s.to {
		if !strings.Contains(recipient, "{{") {
			to = append(to, recipient)
			continue
		}
		rendered, err := renderTemplate(recipient, templateData)
		if err != nil {
			return nil, fmt.Errorf("failed to render recipient template %s: %w", recipient, err)
		}
		// Split by comma in case the template variable contains multiple emails
		for p := range strings.SplitSeq(rendered, ",") {
			trimmed := strings.TrimSpace(p)
			if trimmed == "" || strings.EqualFold(trimmed, "<no value>") {
				continue // skip unresolved or empty values
			}
			to = append(to, trimmed)
		}
	}

	// Normalize and de-duplicate recipients (case-insensitive)
	to = normalizeAndDedupeEmails(to)

	if len(to) == 0 {
		// Diagnostic information: list available keys to help user debug
		keys := make([]string, 0, len(templateData))
		for k := range templateData {
			keys = append(keys, k)
		}
		return nil, fmt.Errorf("no valid recipients found after template resolution (tried: %v, available fields: %v)", s.to, keys)
	}
	return to, nil
}

// setBody renders the body from whichever source the sink is configured with,
// falling back to the formatter when there is no template at all.
func (s *SmtpSink) setBody(ctx context.Context, email *gsmail.Email, msg hermod.Message, templateData map[string]any) error {
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
			if err := setInlineBody(email, s.template, templateData); err != nil {
				return fmt.Errorf("failed to set body: %w", err)
			}
			return nil
		}
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
	return nil
}

// PrepareTemplateData enhances the data map for better template usability.
// It unmarshals 'after' and 'before' JSON strings if present, and flattens 'after' fields.
func PrepareTemplateData(data map[string]any) map[string]any {
	// Try to unmarshal 'after' and 'before' if they are JSON strings
	for _, key := range []string{"after", "before"} {
		if val, ok := data[key]; ok {
			var nested map[string]any
			if str, ok := val.(string); ok && strings.HasPrefix(strings.TrimSpace(str), "{") {
				if err := json.Unmarshal([]byte(str), &nested); err != nil {
					// Try lenient approach
					if cleaned := message.TryFixJSON([]byte(str)); cleaned != nil {
						_ = json.Unmarshal(cleaned, &nested)
					}
				}
				if nested != nil {
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

// SetRetryConfig sets the retry configuration for the SMTP sender.
func (s *SmtpSink) SetRetryConfig(config gsmail.RetryConfig) {
	s.sender.SetRetryConfig(config)
}

// SetPoolConfig enables and configures the connection pool.
func (s *SmtpSink) SetPoolConfig(config gsmailSmtp.PoolConfig) {
	if sender, ok := s.sender.(*gsmailSmtp.Sender); ok {
		sender.EnablePool(config)
	}
}

// SetInsecureSkipVerify toggles TLS verification.
func (s *SmtpSink) SetInsecureSkipVerify(v bool) {
	if sender, ok := s.sender.(*gsmailSmtp.Sender); ok {
		sender.InsecureSkipVerify = v
	}
}

// Close closes the SMTP sink.
func (s *SmtpSink) Close() error {
	if c, ok := s.sender.(interface{ Close() error }); ok {
		return c.Close()
	}
	return nil
}

func renderTemplate(tmplStr string, data any) (string, error) {
	tmpl, err := template.New("smtp").Funcs(templateFuncs).Parse(tmplStr) //nolint:gosec // G708: sink configuration, not message data — see renderTextTemplate
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
	// Release gives up a claim whose work did not complete, so a retry can take
	// it again. Releasing a key that was already marked sent must do nothing,
	// and releasing one never claimed must not be an error.
	Release(ctx context.Context, key string) error
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
