// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/source/webhook"
	mimirwebhook "github.com/gsoultan/mimir-server-sdk/webhook"
)

// A webhook source set to the mimir preset takes the events a mimir-server
// sends: it checks their Mimir-Signature with the webhook secret, dispatches
// each event once however often the server retries it, and keeps only the
// event types it is told to.

const mimirSecret = "whsec_test"

// mimirReceiver is one mimir-preset source with its handler and the channel
// the engine would read.
type mimirReceiver struct {
	t    *testing.T
	mux  *http.ServeMux
	src  *webhook.WebhookSource
	path string
}

func newMimirReceiver(t *testing.T, path string, extra map[string]string) *mimirReceiver {
	t.Helper()
	fullPath := "/api/webhooks/" + path
	cfg := map[string]string{"path": fullPath, "preset": "mimir", "secret": mimirSecret}
	for k, v := range extra {
		cfg[k] = v
	}
	h := NewWebhookHandler(&handlers.Handler{Storage: sourcesOnly{sources: []storage.Source{{Type: "webhook", Config: cfg}}}})
	mux := http.NewServeMux()
	h.RegisterWebhookRoutes(mux)
	src := webhook.NewWebhookSource(fullPath)
	t.Cleanup(func() { _ = src.Close() })
	return &mimirReceiver{t: t, mux: mux, src: src, path: fullPath}
}

// deliver posts body with the given Mimir-Signature header.
func (m *mimirReceiver) deliver(body, signature string) *httptest.ResponseRecorder {
	m.t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(m.t.Context(), http.MethodPost, m.path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if signature != "" {
		req.Header.Set(mimirwebhook.SignatureHeader, signature)
	}
	m.mux.ServeHTTP(rec, req)
	return rec
}

// signed is a delivery signed as the server signs it, now.
func (m *mimirReceiver) signed(body string) *httptest.ResponseRecorder {
	return m.deliver(body, mimirwebhook.Sign(mimirSecret, time.Now(), []byte(body)))
}

// received is what the source read within a short wait, nil when nothing.
func (m *mimirReceiver) received() hermod.Message {
	m.t.Helper()
	ctx, cancel := context.WithTimeout(m.t.Context(), 200*time.Millisecond)
	defer cancel()
	msg, err := m.src.Read(ctx)
	if err != nil {
		return nil
	}
	return msg
}

func event(id, kind string) string {
	b, _ := json.Marshal(map[string]any{
		"id": id, "type": kind, "time": "2026-10-31T10:00:00Z",
		"data": map[string]any{"job": map[string]any{"name": "churn", "accuracy": 0.91}},
	})
	return string(b)
}

func TestAMimirEventIsCheckedByItsSignatureAndDispatchedWithItsTypeAndId(t *testing.T) {
	m := newMimirReceiver(t, "mimir/ok", nil)
	body := event("evt_1", mimirwebhook.TrainingFinished)

	rec := m.signed(body)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("a genuine delivery was answered %d: %s", rec.Code, rec.Body)
	}
	msg := m.received()
	if msg == nil {
		t.Fatal("the event was not dispatched to the source")
	}
	if got := msg.Metadata()["mimir_event"]; got != mimirwebhook.TrainingFinished {
		t.Errorf("mimir_event = %q", got)
	}
	if got := msg.Metadata()["mimir_event_id"]; got != "evt_1" {
		t.Errorf("mimir_event_id = %q", got)
	}
	if string(msg.After()) != body {
		t.Errorf("the message body is %s, want the event as sent", msg.After())
	}
}

func TestAMimirEventWithoutAGoodSignatureIsRefused(t *testing.T) {
	m := newMimirReceiver(t, "mimir/refused", nil)
	body := event("evt_2", mimirwebhook.ModelCreated)
	cases := []struct {
		name, signature, want string
	}{
		{"no signature", "", "Missing Mimir-Signature"},
		{"another secret", mimirwebhook.Sign("whsec_other", time.Now(), []byte(body)), "doesn't match"},
		{"another body", mimirwebhook.Sign(mimirSecret, time.Now(), []byte(`{"id":"x"}`)), "doesn't match"},
		{"stale", mimirwebhook.Sign(mimirSecret, time.Now().Add(-time.Hour), []byte(body)), "too old"},
		{"github style", "sha256=" + strings.Repeat("0", 64), "doesn't match"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := m.deliver(body, tc.signature)
			if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), tc.want) {
				t.Fatalf("%d %s, want 401 saying %q", rec.Code, rec.Body, tc.want)
			}
			if m.received() != nil {
				t.Fatal("a refused delivery was dispatched")
			}
		})
	}
}

func TestARetriedMimirEventIsAcknowledgedButDispatchedOnce(t *testing.T) {
	m := newMimirReceiver(t, "mimir/retry", nil)
	body := event("evt_3", mimirwebhook.AccuracyDropped)

	if rec := m.signed(body); rec.Code != http.StatusAccepted {
		t.Fatalf("first delivery: %d %s", rec.Code, rec.Body)
	}
	if m.received() == nil {
		t.Fatal("the first delivery was not dispatched")
	}
	rec := m.signed(body)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"duplicate"`) {
		t.Fatalf("the retry was answered %d %s, want 200 duplicate", rec.Code, rec.Body)
	}
	if m.received() != nil {
		t.Fatal("the retry was dispatched again")
	}
	// Another event with its own id still goes through.
	if rec := m.signed(event("evt_4", mimirwebhook.AccuracyDropped)); rec.Code != http.StatusAccepted {
		t.Fatalf("a new event after a retry: %d %s", rec.Code, rec.Body)
	}
}

func TestAMimirSourceKeepsOnlyTheEventsItIsToldTo(t *testing.T) {
	m := newMimirReceiver(t, "mimir/filter", map[string]string{"events": "training.finished, model.accuracy_dropped"})

	rec := m.signed(event("evt_5", mimirwebhook.ModelCreated))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ignored"`) {
		t.Fatalf("an unwanted event was answered %d %s, want 200 ignored", rec.Code, rec.Body)
	}
	if m.received() != nil {
		t.Fatal("an unwanted event was dispatched")
	}
	if rec := m.signed(event("evt_6", mimirwebhook.AccuracyDropped)); rec.Code != http.StatusAccepted {
		t.Fatalf("a wanted event was answered %d %s", rec.Code, rec.Body)
	}
	if m.received() == nil {
		t.Fatal("a wanted event was not dispatched")
	}
}

func TestAMimirSourceWithoutASecretRefusesEveryDelivery(t *testing.T) {
	m := newMimirReceiver(t, "mimir/nosecret", map[string]string{"secret": ""})
	rec := m.signed(event("evt_7", mimirwebhook.Test))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "no webhook secret") {
		t.Fatalf("%d %s, want 503 saying the secret is missing", rec.Code, rec.Body)
	}
}

func TestAMimirSourceDoesNotAskForTheGitHubStyleSignature(t *testing.T) {
	// The plain webhook check must step aside: a mimir delivery carries no
	// X-Hub-Signature-256, and the secret is the webhook's, not an HMAC key
	// for the body alone.
	m := newMimirReceiver(t, "mimir/plain", nil)
	if rec := m.signed(event("evt_8", mimirwebhook.Test)); rec.Code != http.StatusAccepted {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestRecentEventsForgetsOldIdsAndBoundsItself(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	c := newRecentEvents()
	c.now = func() time.Time { return now }
	if c.Seen("/p", "a") {
		t.Fatal("a new id was reported seen")
	}
	if !c.Seen("/p", "a") {
		t.Fatal("a repeated id was not reported seen")
	}
	if c.Seen("/q", "a") {
		t.Fatal("the same id on another path counts as seen")
	}
	now = now.Add(seenFor + time.Minute)
	if c.Seen("/p", "a") {
		t.Fatal("an id older than mimir retries was still remembered")
	}
	for i := 0; i < seenLimit+5; i++ {
		c.Seen("/p", strings.Repeat("x", 3)+string(rune('a'+i%26))+time.Duration(i).String())
	}
	if len(c.seen) > seenLimit || len(c.order) > seenLimit {
		t.Errorf("remembers %d ids, want at most %d", len(c.seen), seenLimit)
	}
}
