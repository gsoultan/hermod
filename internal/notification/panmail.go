package notification

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/infra/httpclient"
	"github.com/gsoultan/hermod/pkg/security/secrets"
	sdk "github.com/gsoultan/panmail-sdk"
)

// panmailTimeout bounds one send. The fan-out has its own deadline; this keeps
// one slow gateway from spending all of it.
const panmailTimeout = 15 * time.Second

// Resolver expands the secret and env references in one settings value. The
// registry supplies one that reads the global secret manager; without one,
// only env references (HERMOD_ENV_*) resolve.
//
// The settings are global and only an administrator writes them, so they are
// resolved without a vhost on purpose: a vhost secret of the same name must
// not be able to redirect the gateway, or the key, that every alert uses.
type Resolver func(ctx context.Context, value string) (string, error)

// Literal resolves nothing: every value is used as written.
func Literal(_ context.Context, value string) (string, error) { return value, nil }

func envOnly(ctx context.Context, value string) (string, error) {
	return secrets.ExpandReferences(ctx, nil, "", value)
}

// PanmailMail is one mail to the configured recipients.
type PanmailMail struct {
	Subject string
	Text    string
	HTML    string
	// IdempotencyKey lets the gateway answer a repeated send with the mail it
	// already took, which is what makes a retry after a timeout safe.
	IdempotencyKey string
}

// panmailConfigured reports whether every setting a send needs is present.
func (ns NotificationSettings) panmailConfigured() bool {
	return ns.PanmailURL != "" && ns.PanmailAPIKey != "" && ns.PanmailProviderID != "" &&
		ns.PanmailFrom != "" && (ns.PanmailTo != "" || ns.DefaultEmail != "")
}

// SendPanmail sends mail through the panmail gateway. Every setting may hold
// {{secret.NAME}} or {{env.NAME}} references, which resolve here, at send
// time, so the stored settings never need to hold the api key itself.
func (ns NotificationSettings) SendPanmail(ctx context.Context, resolve Resolver, mail PanmailMail) error {
	if !ns.panmailConfigured() {
		return nil
	}
	if resolve == nil {
		resolve = envOnly
	}

	var errs []error
	get := func(name, value string) string {
		out, err := resolve(ctx, value)
		if err != nil {
			errs = append(errs, fmt.Errorf("panmail %s: %w", name, err))
		}
		return strings.TrimSpace(out)
	}
	baseURL := get("gateway url", ns.PanmailURL)
	apiKey := get("api key", ns.PanmailAPIKey)
	providerID := get("provider", ns.PanmailProviderID)
	from := get("from", ns.PanmailFrom)
	to := ns.PanmailTo
	if to == "" {
		to = ns.DefaultEmail
	}
	recipients := splitAddresses(get("to", to))
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	if len(recipients) == 0 {
		return errors.New("panmail: no recipients")
	}

	client, err := sdk.New(baseURL, apiKey, sdk.WithTimeout(panmailTimeout), sdk.WithTransportRetries(1))
	if err != nil {
		return err
	}
	msg := sdk.Message{
		ProviderID: providerID,
		From:       from,
		To:         recipients,
		Subject:    mail.Subject,
		Text:       mail.Text,
		HTML:       mail.HTML,
	}
	// The SDK only repeats a send whose outcome it does not know when it
	// carries a key; without one, WithTransportRetries does nothing.
	msg.IdempotencyKey = mail.IdempotencyKey
	if _, err := client.Send(ctx, msg); err != nil {
		return httpclient.RedactURLError(err)
	}
	return nil
}

// splitAddresses reads a comma- or semicolon-separated list of addresses.
func splitAddresses(s string) []string {
	fields := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' })
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// sendPanmailAlert is an ordinary alert as a panmail mail.
func (ns NotificationSettings) sendPanmailAlert(ctx context.Context, resolve Resolver, title, message string, wf storage.Workflow) error {
	body := fmt.Sprintf("%s\n\nWorkflow: %s (%s)", message, wf.Name, wf.ID)
	if ns.BaseURL != "" {
		body += fmt.Sprintf("\nDetails: %s/workflows/%s", strings.TrimRight(ns.BaseURL, "/"), wf.ID)
	}
	return ns.SendPanmail(ctx, resolve, PanmailMail{Subject: "[Hermod] " + title, Text: body})
}

// PanmailNotificationProvider sends alerts through the panmail gateway.
type PanmailNotificationProvider struct {
	storage storage.Storage
	resolve Resolver
}

func NewPanmailNotificationProvider(s storage.Storage) *PanmailNotificationProvider {
	return &PanmailNotificationProvider{storage: s}
}

func (p *PanmailNotificationProvider) Send(ctx context.Context, title, message string, wf storage.Workflow) error {
	val, _ := p.storage.GetSetting(ctx, "notification_settings")
	if val == "" {
		return nil
	}
	var settings NotificationSettings
	if err := json.Unmarshal([]byte(val), &settings); err != nil {
		return err
	}
	return settings.sendPanmailAlert(ctx, p.resolve, title, message, wf)
}

func (p *PanmailNotificationProvider) Type() string { return "panmail" }

func (p *PanmailNotificationProvider) SetStorage(s storage.Storage) { p.storage = s }

// SetResolver is how Service.SetResolver reaches this provider.
func (p *PanmailNotificationProvider) SetResolver(r Resolver) { p.resolve = r }
