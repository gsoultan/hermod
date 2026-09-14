package fcm

import (
	"strings"
	"testing"
	"time"
)

func TestFromMap(t *testing.T) {
	cfg, err := FromMap(map[string]string{
		"credentials_json":       `{"type":"service_account","project_id":"demo"}`,
		"topic":                  "orders",
		"title":                  "Order {{.id}}",
		"body":                   "{{.customer}} spent {{.total}}",
		"image_url":              "https://cdn.example.com/{{.sku}}.png",
		"data_mode":              "fields",
		"data_json":              `{"deeplink":"app://orders/{{.id}}"}`,
		"max_data_bytes":         "2048",
		"on_oversize":            "truncate",
		"android_priority":       "high",
		"android_ttl":            "10m",
		"android_channel_id":     "orders",
		"android_sound":          "ding.caf",
		"android_color":          "#ff8800",
		"apns_priority":          "10",
		"apns_expiration":        "5m",
		"apns_badge":             "{{.unread}}",
		"apns_content_available": "true",
		"webpush_link":           "https://app.example.com/orders/{{.id}}",
		"webpush_ttl":            "1h",
		"analytics_label":        "orders_v2",
		"dry_run":                "true",
		"timeout":                "7s",
	})
	if err != nil {
		t.Fatalf("FromMap: %v", err)
	}

	if cfg.Topic != "orders" {
		t.Errorf("Topic = %q, want orders", cfg.Topic)
	}
	if cfg.DataMode != DataFields {
		t.Errorf("DataMode = %q, want %q", cfg.DataMode, DataFields)
	}
	if cfg.Data["deeplink"] != "app://orders/{{.id}}" {
		t.Errorf("Data[deeplink] = %q", cfg.Data["deeplink"])
	}
	if cfg.MaxDataBytes != 2048 {
		t.Errorf("MaxDataBytes = %d, want 2048", cfg.MaxDataBytes)
	}
	if cfg.OnOversize != OversizeTruncate {
		t.Errorf("OnOversize = %q", cfg.OnOversize)
	}
	if cfg.Android.TTL != 10*time.Minute {
		t.Errorf("Android.TTL = %v", cfg.Android.TTL)
	}
	if cfg.APNS.Expiration != 5*time.Minute {
		t.Errorf("APNS.Expiration = %v", cfg.APNS.Expiration)
	}
	if !cfg.APNS.ContentAvailable {
		t.Error("APNS.ContentAvailable = false, want true")
	}
	if cfg.Webpush.TTL != time.Hour {
		t.Errorf("Webpush.TTL = %v", cfg.Webpush.TTL)
	}
	if !cfg.DryRun {
		t.Error("DryRun = false, want true")
	}
	if cfg.Timeout != 7*time.Second {
		t.Errorf("Timeout = %v", cfg.Timeout)
	}
}

// A duration the operator typed wrong must be refused, not silently ignored.
// A parse error that leaves a zero value is how the trace purge stopped running.
func TestFromMapRefusesUnparseableValues(t *testing.T) {
	for _, tc := range []struct{ key, val, want string }{
		{"android_ttl", "10 minutes", "android_ttl"},
		{"timeout", "7", "timeout"},
		{"max_data_bytes", "lots", "max_data_bytes"},
		{"data_json", "not json", "data_json"},
		{"on_oversize", "explode", "on_oversize"},
		{"data_mode", "everything", "data_mode"},
		{"action", "delete", "action"},
		{"apns_priority", "urgent", "apns_priority"},
		{"android_priority", "urgent", "android_priority"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			_, err := FromMap(map[string]string{tc.key: tc.val})
			if err == nil {
				t.Fatalf("FromMap(%s=%q) = nil error, want a refusal", tc.key, tc.val)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not name %q", err, tc.want)
			}
		})
	}
}

func TestNewRefusesMoreThanOneDefaultTarget(t *testing.T) {
	// FCM accepts exactly one of token, topic and condition. Setting two
	// defaults used to produce a message with all three populated, which the
	// SDK refuses on every single send.
	_, err := New(Config{
		CredentialsJSON: serviceAccountJSON,
		Token:           "device-a",
		Topic:           "orders",
	})
	if err == nil {
		t.Fatal("New with both a default token and a default topic = nil error, want a refusal")
	}
	if !strings.Contains(err.Error(), "exactly one") {
		t.Errorf("error %q does not explain the one-target rule", err)
	}
}

func TestNewRefusesBrokenTemplate(t *testing.T) {
	_, err := New(Config{
		CredentialsJSON: serviceAccountJSON,
		Topic:           "orders",
		Title:           "Order {{.id",
	})
	if err == nil {
		t.Fatal("New with an unclosed action = nil error, want a refusal at construction")
	}
	if !strings.Contains(err.Error(), "title") {
		t.Errorf("error %q does not name the offending field", err)
	}
}

func TestNewRefusesCredentiallessSinkWithoutExplicitOptIn(t *testing.T) {
	// An empty credentials_json silently fell through to Application Default
	// Credentials, so a developer machine with gcloud logged in would push to
	// whatever project that account defaults to.
	_, err := New(Config{Topic: "orders"})
	if err == nil {
		t.Fatal("New with no credentials = nil error, want a refusal")
	}
	if !strings.Contains(err.Error(), "credentials") {
		t.Errorf("error %q does not mention credentials", err)
	}

	if _, err := New(Config{
		Topic:                 "orders",
		UseDefaultCredentials: true,
		ProjectID:             "demo",
	}); err != nil {
		t.Errorf("New with an explicit ADC opt-in and a project id: %v", err)
	}
}
