// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package notification

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"sync"
	"time"

	"github.com/gsoultan/gsmail"
	"github.com/gsoultan/gsmail/smtp"
	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/infra/httpclient"
)

// httpClient is the one client every webhook-style channel sends on.
//
// These calls used http.DefaultClient — which has no timeout — on a context
// that carries no deadline, and Notify ran them inline on whichever engine
// goroutine raised the status change. A destination that accepted the
// connection and then said nothing therefore blocked the pipeline, and the
// blocking was worst during an incident, when the alerts fire.
var httpClient = &http.Client{Timeout: 15 * time.Second}

// telegramAPIBase is the Telegram Bot API root. Only tests write it; the send
// path had the host baked into a format string, which left no way to exercise
// it without talking to Telegram.
var telegramAPIBase = "https://api.telegram.org"

// notifyTimeout bounds one fan-out across every configured provider.
const notifyTimeout = 30 * time.Second

type NotificationSettings struct {
	SMTPHost     string `json:"smtp_host"`
	SMTPPort     int    `json:"smtp_port"`
	SMTPUser     string `json:"smtp_user"`
	SMTPPassword string `json:"smtp_password"`
	SMTPFrom     string `json:"smtp_from"`
	SMTPSSL      bool   `json:"smtp_ssl"`
	DefaultEmail string `json:"default_email"`

	TelegramToken  string `json:"telegram_token"`
	TelegramChatID string `json:"telegram_chat_id"`

	SlackWebhook   string `json:"slack_webhook"`
	DiscordWebhook string `json:"discord_webhook"`
	WebhookURL     string `json:"webhook_url"`
	BaseURL        string `json:"base_url"`

	// Panmail sends alerts through a panmail gateway. Each value may hold
	// {{secret.NAME}} or {{env.NAME}} references, resolved at send time.
	PanmailURL        string `json:"panmail_url"`
	PanmailAPIKey     string `json:"panmail_api_key"`
	PanmailProviderID string `json:"panmail_provider_id"`
	PanmailFrom       string `json:"panmail_from"`
	// PanmailTo is a comma-separated list. Empty means DefaultEmail.
	PanmailTo string `json:"panmail_to"`
	// PanmailDeadLetters mails a digest of every dead-lettered message: the
	// workflow, the node or sink, the error and the message, batched per
	// workflow over DeadLetterDigestWindow.
	PanmailDeadLetters bool `json:"panmail_dead_letters"`
	// PanmailHidePayload leaves message contents out of those digests.
	PanmailHidePayload bool `json:"panmail_hide_payload"`

	// EmailDeadLetters and TelegramDeadLetters send the same dead-letter
	// digest through SMTP and Telegram.
	EmailDeadLetters    bool `json:"email_dead_letters"`
	TelegramDeadLetters bool `json:"telegram_dead_letters"`
	// DeadLetterHidePayload leaves message contents out of the digest on
	// every channel.
	DeadLetterHidePayload bool `json:"dead_letter_hide_payload"`
}

// smtpConfigured reports whether email has a server and a recipient.
func (ns NotificationSettings) smtpConfigured() bool {
	return ns.SMTPHost != "" && ns.DefaultEmail != ""
}

// telegramConfigured reports whether Telegram has a bot and a chat.
func (ns NotificationSettings) telegramConfigured() bool {
	return ns.TelegramToken != "" && ns.TelegramChatID != ""
}

// smtpSend sends one email with ns's SMTP settings. Tests replace it.
var smtpSend = func(ctx context.Context, ns NotificationSettings, email gsmail.Email) error {
	return smtp.NewSender(ns.SMTPHost, ns.SMTPPort, ns.SMTPUser, ns.SMTPPassword, ns.SMTPSSL).Send(ctx, email)
}

type TestResult struct {
	Channel string `json:"channel"`
	Status  string `json:"status"` // ok | skipped | error
	Error   string `json:"error,omitempty"`
}

func (ns NotificationSettings) Test(ctx context.Context) []TestResult {
	return ns.TestWith(ctx, nil)
}

// TestWith is Test with the resolver panmail's references are read through.
func (ns NotificationSettings) TestWith(ctx context.Context, resolve Resolver) []TestResult {
	wf := storage.Workflow{ID: "test", Name: "Test Notification"}
	results := make([]TestResult, 0, 6)

	// Helper to execute and capture result
	exec := func(ch string, enabled bool, send func() error) {
		if !enabled {
			results = append(results, TestResult{Channel: ch, Status: "skipped"})
			return
		}
		if err := send(); err != nil {
			results = append(results, TestResult{Channel: ch, Status: "error", Error: err.Error()})
		} else {
			results = append(results, TestResult{Channel: ch, Status: "ok"})
		}
	}

	exec("email", ns.SMTPHost != "" && ns.DefaultEmail != "", func() error {
		return ns.SendEmail(ctx, "Hermod Test", "This is a test notification.", wf)
	})
	exec("slack", ns.SlackWebhook != "", func() error {
		return ns.SendSlack(ctx, "Hermod Test", "This is a test notification.", wf)
	})
	exec("discord", ns.DiscordWebhook != "", func() error {
		return ns.SendDiscord(ctx, "Hermod Test", "This is a test notification.", wf)
	})
	exec("webhook", ns.WebhookURL != "", func() error {
		return ns.SendGenericWebhook(ctx, "Hermod Test", "This is a test notification.", wf)
	})
	exec("telegram", ns.TelegramToken != "" && ns.TelegramChatID != "", func() error {
		return ns.SendTelegram(ctx, "Hermod Test", "This is a test notification.", wf)
	})
	exec("panmail", ns.panmailConfigured(), func() error {
		return ns.sendPanmailAlert(ctx, resolve, "Hermod Test", "This is a test notification.", wf)
	})

	return results
}

type Provider interface {
	Send(ctx context.Context, title, message string, wf storage.Workflow) error
	Type() string
	SetStorage(s storage.Storage)
}

// Severity levels for an alert. They exist because not every notification is a
// fault: a workflow an operator stopped, or a worker draining during a deploy,
// is routine. UINotificationProvider wrote every alert at ERROR, so adding
// lifecycle notifications would have filed routine events as errors in the log
// table and in the UI's error view.
const (
	LevelInfo  = "INFO"
	LevelWarn  = "WARN"
	LevelError = "ERROR"
)

// LeveledProvider is the optional interface for a provider that records a
// severity. Providers that push to a human (Telegram, Slack, email) do not need
// one — the title carries the meaning there — so the interface stays optional
// rather than widening Provider for a single implementation.
type LeveledProvider interface {
	SendLeveled(ctx context.Context, level, title, message string, wf storage.Workflow) error
}

type Service struct {
	providers   []Provider
	storage     storage.Storage
	lastSent    map[string]time.Time
	logger      hermod.Logger
	resolve     Resolver
	deadLetters *deadLetterDigest
	inflight    inflight
	mu          sync.RWMutex
	// now is the clock the dedupe window is measured by; tests set it.
	now func() time.Time
	// admitted counts dedupe entries added since the last sweep of expired
	// ones, and lastSweep is when that was.
	admitted  int
	lastSweep time.Time
}

// dedupeWindow is how long a repeat of the same alert for the same workflow
// is swallowed.
const dedupeWindow = 5 * time.Minute

// dedupeSweepEvery is how many new dedupe entries are admitted between sweeps
// of the ones whose window has passed.
const dedupeSweepEvery = 256

// inflight counts dispatched notifications still sending.
//
// It replaces a sync.WaitGroup, which forbids an Add from zero while a Wait is
// in progress. Alerts are raised from any goroutine — the ML drift monitor has
// its own — so a shutdown or a test waiting for them could not rule that out,
// and the race detector caught it.
type inflight struct {
	mu   sync.Mutex
	n    int
	idle chan struct{}
}

func (f *inflight) add() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.n == 0 {
		f.idle = make(chan struct{})
	}
	f.n++
}

func (f *inflight) done() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n--
	if f.n == 0 {
		close(f.idle)
	}
}

// wait returns a channel closed once nothing dispatched before the call is
// still sending.
func (f *inflight) wait() <-chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.n == 0 {
		c := make(chan struct{})
		close(c)
		return c
	}
	return f.idle
}

func NewService(s storage.Storage) *Service {
	return &Service{
		storage:     s,
		lastSent:    make(map[string]time.Time),
		deadLetters: newDeadLetterDigest(),
		now:         time.Now,
	}
}

func (s *Service) AddProvider(p Provider) {
	s.providers = append(s.providers, p)
}

// SetLogger routes provider failures to the process log.
//
// They went to fmt.Printf, so a rejected Telegram token or a webhook returning
// 500 appeared on stdout and nowhere else: not in the structured log, not in
// the log table, not in the UI. The one thing an operator needs to know about
// an alerting channel is that it has stopped working.
func (s *Service) SetLogger(l hermod.Logger) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logger = l
}

// resolverSetter is a provider whose settings may hold references.
type resolverSetter interface {
	SetResolver(Resolver)
}

// SetResolver sets how secret and env references in the settings resolve.
// Call it while wiring up, before alerts flow: providers read it unlocked.
func (s *Service) SetResolver(r Resolver) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resolve = r
	for _, p := range s.providers {
		if rs, ok := p.(resolverSetter); ok {
			rs.SetResolver(r)
		}
	}
}

// Wait blocks until every dispatched notification has finished. It exists for
// shutdown and for tests; the send path itself never waits.
func (s *Service) Wait() {
	<-s.inflight.wait()
}

// WaitFor blocks until in-flight notifications finish or d elapses, and reports
// whether they finished.
//
// Shutdown needs this. The fan-out runs on its own goroutine, so the alert
// saying a worker is going down was raced by the process exiting — dispatched,
// never sent. Waiting without a bound is the opposite mistake: an unreachable
// channel would hold the shutdown open for the full send timeout.
func (s *Service) WaitFor(d time.Duration) bool {
	done := s.inflight.wait()
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

func (s *Service) SetStorage(s2 storage.Storage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.storage = s2
	for _, p := range s.providers {
		p.SetStorage(s2)
	}
}

// Notify raises an alert at ERROR. Most alerts are faults; the ones that are
// not go through NotifyLevel.
func (s *Service) Notify(ctx context.Context, title, message string, wf storage.Workflow) {
	s.NotifyLevel(ctx, LevelError, title, message, wf)
}

// sweepDedupeLocked drops the dedupe entries whose window has passed: once
// every dedupeSweepEvery admissions on a busy service, and at least once a
// window on a quiet one. The caller holds s.mu.
//
// lastSent is keyed by workflow ID and title and nothing removed an entry, so a
// deployment that creates and deletes workflows grew it for the life of the
// process. An entry older than the window can never swallow anything again,
// and a sweep amortised over admissions keeps the map proportional to the
// alerts of the last five minutes rather than to every workflow ever alerted on.
func (s *Service) sweepDedupeLocked(now time.Time) {
	s.admitted++
	if s.admitted < dedupeSweepEvery && now.Sub(s.lastSweep) < dedupeWindow {
		return
	}
	s.admitted, s.lastSweep = 0, now
	for key, last := range s.lastSent {
		if now.Sub(last) >= dedupeWindow {
			delete(s.lastSent, key)
		}
	}
}

// NotifyLevel raises an alert at an explicit severity.
func (s *Service) NotifyLevel(ctx context.Context, level, title, message string, wf storage.Workflow) {
	s.mu.Lock()
	// No database yet -- a first run before setup finishes. There are no
	// channel settings to read and nowhere to record the alert, and every
	// provider would dereference the missing storage. Checked before the repeat
	// window below, so a dropped alert does not silence the first real one.
	if s.storage == nil {
		s.mu.Unlock()
		return
	}
	now := s.now()
	key := wf.ID + ":" + title
	if last, ok := s.lastSent[key]; ok {
		if now.Sub(last) < dedupeWindow {
			s.mu.Unlock()
			return
		}
	}
	s.lastSent[key] = now
	s.sweepDedupeLocked(now)
	providers := s.providers
	logger := s.logger
	s.mu.Unlock()

	// Detached from the caller's context, not derived from it. Notify runs on
	// the engine's status-change callback, and the contexts reaching it are
	// cancelled by the very events worth alerting on — an engine that stops
	// cancels its context before the "stopped" alert can leave the process.
	// The deadline below is what bounds the work instead.
	sendCtx := context.WithoutCancel(ctx)

	s.inflight.add()
	go func() {
		defer s.inflight.done()
		ctx, cancel := context.WithTimeout(sendCtx, notifyTimeout)
		defer cancel()

		for _, p := range providers {
			var err error
			if lp, ok := p.(LeveledProvider); ok {
				err = lp.SendLeveled(ctx, level, title, message, wf)
			} else {
				err = p.Send(ctx, title, message, wf)
			}
			if err != nil {
				if logger != nil {
					logger.Error("Notification channel failed",
						"channel", p.Type(),
						"workflow_id", wf.ID,
						"title", title,
						"error", err.Error())
				}
			}
		}
	}()
}

type UINotificationProvider struct {
	storage storage.Storage
}

func NewUINotificationProvider(s storage.Storage) *UINotificationProvider {
	return &UINotificationProvider{storage: s}
}

func (p *UINotificationProvider) Send(ctx context.Context, title, message string, wf storage.Workflow) error {
	return p.SendLeveled(ctx, LevelError, title, message, wf)
}

// SendLeveled records the alert in the log table at the severity it was raised
// with, so a routine stop does not read as a failure.
func (p *UINotificationProvider) SendLeveled(ctx context.Context, level, title, message string, wf storage.Workflow) error {
	if level == "" {
		level = LevelError
	}
	log := storage.Log{
		Timestamp:  time.Now(),
		Level:      level,
		Message:    message,
		Action:     "NOTIFICATION",
		WorkflowID: wf.ID,
		Data:       title,
	}
	return p.storage.CreateLog(ctx, log)
}

func (p *UINotificationProvider) Type() string {
	return "ui"
}

func (p *UINotificationProvider) SetStorage(s storage.Storage) {
	p.storage = s
}

type EmailNotificationProvider struct {
	storage storage.Storage
}

func NewEmailNotificationProvider(s storage.Storage) *EmailNotificationProvider {
	return &EmailNotificationProvider{storage: s}
}

func (p *EmailNotificationProvider) Send(ctx context.Context, title, message string, wf storage.Workflow) error {
	val, _ := p.storage.GetSetting(ctx, "notification_settings")
	if val == "" {
		return nil
	}

	var settings NotificationSettings
	if err := json.Unmarshal([]byte(val), &settings); err != nil {
		return err
	}

	return settings.SendEmail(ctx, title, message, wf)
}

func (ns NotificationSettings) SendEmail(ctx context.Context, title, message string, wf storage.Workflow) error {
	if ns.SMTPHost == "" || ns.DefaultEmail == "" {
		return nil
	}

	body := fmt.Sprintf("%s\n\nWorkflow: %s (%s)", message, wf.Name, wf.ID)
	if ns.BaseURL != "" {
		body += fmt.Sprintf("\nDetails: %s/workflows/%s", ns.BaseURL, wf.ID)
	}

	email := gsmail.Email{
		From:    ns.SMTPFrom,
		To:      []string{ns.DefaultEmail},
		Subject: title,
		Body:    []byte(body),
	}

	return smtpSend(ctx, ns, email)
}

func (p *EmailNotificationProvider) Type() string {
	return "email"
}

func (p *EmailNotificationProvider) SetStorage(s storage.Storage) {
	p.storage = s
}

type TelegramNotificationProvider struct {
	storage storage.Storage
}

func NewTelegramNotificationProvider(s storage.Storage) *TelegramNotificationProvider {
	return &TelegramNotificationProvider{storage: s}
}

func (p *TelegramNotificationProvider) Send(ctx context.Context, title, message string, wf storage.Workflow) error {
	val, _ := p.storage.GetSetting(ctx, "notification_settings")
	if val == "" {
		return nil
	}

	var settings NotificationSettings
	if err := json.Unmarshal([]byte(val), &settings); err != nil {
		return err
	}

	return settings.SendTelegram(ctx, title, message, wf)
}

func (ns NotificationSettings) SendTelegram(ctx context.Context, title, message string, wf storage.Workflow) error {
	if ns.TelegramToken == "" || ns.TelegramChatID == "" {
		return nil
	}

	// HTML, not Markdown, and every interpolated value escaped.
	//
	// The body carries a raw Go error string, and those are full of Markdown's
	// active characters: pq names tables like user_events, drivers quote
	// identifiers, workflow names are whatever an operator typed. Telegram
	// rejects a message whose entities do not balance with 400 "can't parse
	// entities", so the alert was dropped by exactly the errors most worth
	// sending. HTML has three metacharacters and they are all escapable, so
	// * and _ arrive as themselves.
	text := fmt.Sprintf("<b>%s</b>\n%s\nWorkflow: %s",
		html.EscapeString(title), html.EscapeString(message), html.EscapeString(wf.Name))
	if ns.BaseURL != "" {
		text += fmt.Sprintf("\n<a href=\"%s/workflows/%s\">View Details</a>",
			html.EscapeString(ns.BaseURL), html.EscapeString(wf.ID))
	}
	return ns.sendTelegramHTML(ctx, text)
}

// sendTelegramHTML sends text, already HTML-escaped, to the configured chat.
func (ns NotificationSettings) sendTelegramHTML(ctx context.Context, text string) error {
	apiURL := fmt.Sprintf("%s/bot%s/sendMessage", telegramAPIBase, ns.TelegramToken)
	body, _ := json.Marshal(map[string]string{
		"chat_id":    ns.TelegramChatID,
		"text":       text,
		"parse_mode": "HTML",
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewBuffer(body))
	if err != nil {
		return httpclient.RedactURLError(err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return httpclient.RedactURLError(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var result map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&result)
		return fmt.Errorf("telegram api returned status: %d, error: %v", resp.StatusCode, result["description"])
	}

	return nil
}

func (p *TelegramNotificationProvider) Type() string {
	return "telegram"
}

func (p *TelegramNotificationProvider) SetStorage(s storage.Storage) {
	p.storage = s
}

type SlackNotificationProvider struct {
	storage storage.Storage
}

func NewSlackNotificationProvider(s storage.Storage) *SlackNotificationProvider {
	return &SlackNotificationProvider{storage: s}
}

func (p *SlackNotificationProvider) Send(ctx context.Context, title, message string, wf storage.Workflow) error {
	val, _ := p.storage.GetSetting(ctx, "notification_settings")
	if val == "" {
		return nil
	}

	var settings NotificationSettings
	if err := json.Unmarshal([]byte(val), &settings); err != nil {
		return err
	}

	return settings.SendSlack(ctx, title, message, wf)
}

func (ns NotificationSettings) SendSlack(ctx context.Context, title, message string, wf storage.Workflow) error {
	if ns.SlackWebhook == "" {
		return nil
	}

	text := fmt.Sprintf("*%s*\n%s\nWorkflow: %s", title, message, wf.Name)
	if ns.BaseURL != "" {
		text += fmt.Sprintf("\n<%s/workflows/%s|View Details>", ns.BaseURL, wf.ID)
	}

	body, _ := json.Marshal(map[string]any{
		"text": text,
		"attachments": []map[string]any{
			{
				"color": "#ff0000",
				"fields": []map[string]any{
					{"title": "Workflow", "value": wf.Name, "short": true},
					{"title": "ID", "value": wf.ID, "short": true},
					{"title": "Status", "value": "Error", "short": true},
				},
			},
		},
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ns.SlackWebhook, bytes.NewBuffer(body))
	if err != nil {
		return httpclient.RedactURLError(err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return httpclient.RedactURLError(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("slack api returned status: %d", resp.StatusCode)
	}

	return nil
}

func (p *SlackNotificationProvider) Type() string {
	return "slack"
}

func (p *SlackNotificationProvider) SetStorage(s storage.Storage) {
	p.storage = s
}

type DiscordNotificationProvider struct {
	storage storage.Storage
}

func NewDiscordNotificationProvider(s storage.Storage) *DiscordNotificationProvider {
	return &DiscordNotificationProvider{storage: s}
}

func (p *DiscordNotificationProvider) Send(ctx context.Context, title, message string, wf storage.Workflow) error {
	val, _ := p.storage.GetSetting(ctx, "notification_settings")
	if val == "" {
		return nil
	}

	var settings NotificationSettings
	if err := json.Unmarshal([]byte(val), &settings); err != nil {
		return err
	}

	return settings.SendDiscord(ctx, title, message, wf)
}

func (ns NotificationSettings) SendDiscord(ctx context.Context, title, message string, wf storage.Workflow) error {
	if ns.DiscordWebhook == "" {
		return nil
	}

	content := fmt.Sprintf("**%s**\n%s\nWorkflow: %s", title, message, wf.Name)
	if ns.BaseURL != "" {
		content += fmt.Sprintf("\n[View Details](%s/workflows/%s)", ns.BaseURL, wf.ID)
	}

	body, _ := json.Marshal(map[string]any{
		"content": content,
		"embeds": []map[string]any{
			{
				"title":       title,
				"description": message,
				"color":       16711680, // Red
				"fields": []map[string]any{
					{"name": "Workflow", "value": wf.Name, "inline": true},
					{"name": "ID", "value": wf.ID, "inline": true},
				},
			},
		},
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ns.DiscordWebhook, bytes.NewBuffer(body))
	if err != nil {
		return httpclient.RedactURLError(err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return httpclient.RedactURLError(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("discord api returned status: %d", resp.StatusCode)
	}

	return nil
}

func (p *DiscordNotificationProvider) Type() string {
	return "discord"
}

func (p *DiscordNotificationProvider) SetStorage(s storage.Storage) {
	p.storage = s
}

type GenericWebhookProvider struct {
	storage storage.Storage
}

func NewGenericWebhookProvider(s storage.Storage) *GenericWebhookProvider {
	return &GenericWebhookProvider{storage: s}
}

func (p *GenericWebhookProvider) Send(ctx context.Context, title, message string, wf storage.Workflow) error {
	val, _ := p.storage.GetSetting(ctx, "notification_settings")
	if val == "" {
		return nil
	}

	var settings NotificationSettings
	if err := json.Unmarshal([]byte(val), &settings); err != nil {
		return err
	}

	return settings.SendGenericWebhook(ctx, title, message, wf)
}

func (ns NotificationSettings) SendGenericWebhook(ctx context.Context, title, message string, wf storage.Workflow) error {
	if ns.WebhookURL == "" {
		return nil
	}

	data := map[string]any{
		"title":       title,
		"message":     message,
		"workflow_id": wf.ID,
		"name":        wf.Name,
		"timestamp":   time.Now().Format(time.RFC3339),
	}

	if ns.BaseURL != "" {
		data["details_url"] = fmt.Sprintf("%s/workflows/%s", ns.BaseURL, wf.ID)
	}

	body, _ := json.Marshal(data)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ns.WebhookURL, bytes.NewBuffer(body))
	if err != nil {
		return httpclient.RedactURLError(err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return httpclient.RedactURLError(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook api returned status: %d", resp.StatusCode)
	}

	return nil
}

func (p *GenericWebhookProvider) Type() string {
	return "webhook"
}

func (p *GenericWebhookProvider) SetStorage(s storage.Storage) {
	p.storage = s
}
