package notification

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gsoultan/gsmail"
	"github.com/gsoultan/gsmail/smtp"
	"github.com/user/hermod/internal/storage"
)

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
}

type Provider interface {
	Send(ctx context.Context, title, message string, wf storage.Workflow) error
	Type() string
}

type Service struct {
	providers []Provider
	storage   storage.Storage
	lastSent  map[string]time.Time
	mu        sync.RWMutex
}

func NewService(s storage.Storage) *Service {
	return &Service{
		storage:  s,
		lastSent: make(map[string]time.Time),
	}
}

func (s *Service) AddProvider(p Provider) {
	s.providers = append(s.providers, p)
}

func (s *Service) Notify(ctx context.Context, title, message string, wf storage.Workflow) {
	s.mu.Lock()
	key := wf.ID + ":" + title
	if last, ok := s.lastSent[key]; ok {
		if time.Since(last) < 5*time.Minute {
			s.mu.Unlock()
			return
		}
	}
	s.lastSent[key] = time.Now()
	s.mu.Unlock()

	for _, p := range s.providers {
		err := p.Send(ctx, title, message, wf)
		if err != nil {
			fmt.Printf("Failed to send notification via %s: %v\n", p.Type(), err)
		}
	}
}

type UINotificationProvider struct {
	storage storage.Storage
}

func NewUINotificationProvider(s storage.Storage) *UINotificationProvider {
	return &UINotificationProvider{storage: s}
}

func (p *UINotificationProvider) Send(ctx context.Context, title, message string, wf storage.Workflow) error {
	log := storage.Log{
		Timestamp:  time.Now(),
		Level:      "ERROR",
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

type EmailNotificationProvider struct {
	storage storage.Storage
}

func NewEmailNotificationProvider(s storage.Storage) *EmailNotificationProvider {
	return &EmailNotificationProvider{storage: s}
}

func (p *EmailNotificationProvider) Send(ctx context.Context, title, message string, wf storage.Workflow) error {
	val, err := p.storage.GetSetting(ctx, "notification_settings")
	if err != nil || val == "" {
		return nil
	}

	var settings NotificationSettings
	if err := json.Unmarshal([]byte(val), &settings); err != nil {
		return err
	}

	if settings.SMTPHost == "" || settings.DefaultEmail == "" {
		return nil
	}

	sender := smtp.NewSender(settings.SMTPHost, settings.SMTPPort, settings.SMTPUser, settings.SMTPPassword, settings.SMTPSSL)

	email := gsmail.Email{
		From:    settings.SMTPFrom,
		To:      []string{settings.DefaultEmail},
		Subject: title,
		Body:    []byte(fmt.Sprintf("%s\n\nWorkflow: %s (%s)", message, wf.Name, wf.ID)),
	}

	return sender.Send(ctx, email)
}

func (p *EmailNotificationProvider) Type() string {
	return "email"
}

type TelegramNotificationProvider struct {
	storage storage.Storage
}

func NewTelegramNotificationProvider(s storage.Storage) *TelegramNotificationProvider {
	return &TelegramNotificationProvider{storage: s}
}

func (p *TelegramNotificationProvider) Send(ctx context.Context, title, message string, wf storage.Workflow) error {
	val, err := p.storage.GetSetting(ctx, "notification_settings")
	if err != nil || val == "" {
		return nil
	}

	var settings NotificationSettings
	if err := json.Unmarshal([]byte(val), &settings); err != nil {
		return err
	}

	if settings.TelegramToken == "" || settings.TelegramChatID == "" {
		return nil
	}

	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", settings.TelegramToken)
	body, _ := json.Marshal(map[string]string{
		"chat_id":    settings.TelegramChatID,
		"text":       fmt.Sprintf("*%s*\n%s\nWorkflow: %s", title, message, wf.Name),
		"parse_mode": "Markdown",
	})

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var result map[string]interface{}
		_ = json.NewDecoder(resp.Body).Decode(&result)
		return fmt.Errorf("telegram api returned status: %d, error: %v", resp.StatusCode, result["description"])
	}

	return nil
}

func (p *TelegramNotificationProvider) Type() string {
	return "telegram"
}

type SlackNotificationProvider struct {
	storage storage.Storage
}

func NewSlackNotificationProvider(s storage.Storage) *SlackNotificationProvider {
	return &SlackNotificationProvider{storage: s}
}

func (p *SlackNotificationProvider) Send(ctx context.Context, title, message string, wf storage.Workflow) error {
	val, err := p.storage.GetSetting(ctx, "notification_settings")
	if err != nil || val == "" {
		return nil
	}

	var settings NotificationSettings
	if err := json.Unmarshal([]byte(val), &settings); err != nil {
		return err
	}

	if settings.SlackWebhook == "" {
		return nil
	}

	body, _ := json.Marshal(map[string]interface{}{
		"text": fmt.Sprintf("*%s*\n%s\nWorkflow: %s", title, message, wf.Name),
		"attachments": []map[string]interface{}{
			{
				"color": "#ff0000",
				"fields": []map[string]interface{}{
					{"title": "Workflow", "value": wf.Name, "short": true},
					{"title": "ID", "value": wf.ID, "short": true},
					{"title": "Status", "value": "Error", "short": true},
				},
			},
		},
	})

	req, err := http.NewRequestWithContext(ctx, "POST", settings.SlackWebhook, bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
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

type DiscordNotificationProvider struct {
	storage storage.Storage
}

func NewDiscordNotificationProvider(s storage.Storage) *DiscordNotificationProvider {
	return &DiscordNotificationProvider{storage: s}
}

func (p *DiscordNotificationProvider) Send(ctx context.Context, title, message string, wf storage.Workflow) error {
	val, err := p.storage.GetSetting(ctx, "notification_settings")
	if err != nil || val == "" {
		return nil
	}

	var settings NotificationSettings
	if err := json.Unmarshal([]byte(val), &settings); err != nil {
		return err
	}

	if settings.DiscordWebhook == "" {
		return nil
	}

	body, _ := json.Marshal(map[string]interface{}{
		"content": fmt.Sprintf("**%s**\n%s\nWorkflow: %s", title, message, wf.Name),
		"embeds": []map[string]interface{}{
			{
				"title":       title,
				"description": message,
				"color":       16711680, // Red
				"fields": []map[string]interface{}{
					{"name": "Workflow", "value": wf.Name, "inline": true},
					{"name": "ID", "value": wf.ID, "inline": true},
				},
			},
		},
	})

	req, err := http.NewRequestWithContext(ctx, "POST", settings.DiscordWebhook, bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
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

type GenericWebhookProvider struct {
	storage storage.Storage
}

func NewGenericWebhookProvider(s storage.Storage) *GenericWebhookProvider {
	return &GenericWebhookProvider{storage: s}
}

func (p *GenericWebhookProvider) Send(ctx context.Context, title, message string, wf storage.Workflow) error {
	val, err := p.storage.GetSetting(ctx, "notification_settings")
	if err != nil || val == "" {
		return nil
	}

	var settings NotificationSettings
	if err := json.Unmarshal([]byte(val), &settings); err != nil {
		return err
	}

	if settings.WebhookURL == "" {
		return nil
	}

	body, _ := json.Marshal(map[string]interface{}{
		"title":       title,
		"message":     message,
		"workflow_id": wf.ID,
		"name":        wf.Name,
		"timestamp":   time.Now().Format(time.RFC3339),
	})

	req, err := http.NewRequestWithContext(ctx, "POST", settings.WebhookURL, bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
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
