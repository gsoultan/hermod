package factory

import (
	"strings"
	"testing"

	"github.com/gsoultan/hermod"
)

// panmailConfig is a sink that works, so each test can break exactly one thing.
func panmailConfig() SinkConfig {
	return SinkConfig{
		ID:   "panmail-1",
		Type: "panmail",
		Config: hermod.StringMap{
			"base_url":    "https://mail.example.com",
			"api_key":     "key-123",
			"provider_id": "prov-1",
			"from":        "noreply@example.com",
			"to":          "{{.email}}",
			"subject":     "Order {{.id}}",
			"html":        "<p>Thanks.</p>",
		},
	}
}

// The UI offers allowed_hosts; this is the test that the factory reads the same
// key, so a list typed into the form actually bounds the sink rather than
// leaving it refusing to start.
func TestCreateSink_PanmailReadsAllowedHosts(t *testing.T) {
	cfg := panmailConfig()
	cfg.Config["base_url"] = "https://{{.tenant}}.mail.example.com"

	if _, err := createSinkBase(cfg); err == nil {
		t.Fatal("a templated gateway url was accepted with no allowed_hosts")
	} else if !strings.Contains(err.Error(), "allowed hosts") {
		t.Fatalf("error %q does not say what is missing", err)
	}

	cfg.Config["allowed_hosts"] = "*.mail.example.com, mail.example.com"
	if _, err := createSinkBase(cfg); err != nil {
		t.Fatalf("createSinkBase with allowed_hosts: %v", err)
	}
}

// A templated api key is the same decision as a templated gateway: it says a
// row supplies the credential, and the allowlist is what bounds where it goes.
func TestCreateSink_PanmailTemplatedAPIKeyNeedsAllowedHosts(t *testing.T) {
	cfg := panmailConfig()
	cfg.Config["api_key"] = "{{.tenant_key}}"

	if _, err := createSinkBase(cfg); err == nil {
		t.Fatal("a templated api key was accepted with no allowed_hosts")
	}

	cfg.Config["allowed_hosts"] = "mail.example.com"
	if _, err := createSinkBase(cfg); err != nil {
		t.Fatalf("createSinkBase with allowed_hosts: %v", err)
	}
}

// Nothing above changes the ordinary case: a fixed gateway needs no allowlist.
func TestCreateSink_PanmailStaticGatewayNeedsNoAllowlist(t *testing.T) {
	if _, err := createSinkBase(panmailConfig()); err != nil {
		t.Fatalf("createSinkBase: %v", err)
	}
}
