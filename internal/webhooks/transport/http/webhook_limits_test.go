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
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/source/webhook"
	"github.com/klauspost/compress/zstd"
)

// The webhook endpoint is public: anyone who can reach the server can call
// any path under /api/webhooks/. These tests hold it to what a public endpoint
// must not do — read without limit, inflate without limit, write to the
// database for a caller it has not authenticated, or keep a caller's
// credentials in plain text.

// savingStore records the webhook requests the handler saves.
type savingStore struct {
	sourcesOnly
	mu    sync.Mutex
	saved []storage.WebhookRequest
}

func (s *savingStore) CreateWebhookRequest(_ context.Context, req storage.WebhookRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saved = append(s.saved, req)
	return nil
}

func (s *savingStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.saved)
}

func newSavingStore(fullPath string, config map[string]string) *savingStore {
	cfg := map[string]string{"path": fullPath}
	for k, v := range config {
		cfg[k] = v
	}
	return &savingStore{sourcesOnly: sourcesOnly{sources: []storage.Source{{Type: "webhook", Config: cfg}}}}
}

// post sends body to fullPath with a listening source and maxBody as the
// handler's limit (0 keeps the default).
func postLimited(t *testing.T, store storage.Storage, fullPath string, maxBody int64, body []byte, header http.Header) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	h := NewWebhookHandler(&handlers.Handler{Storage: store})
	h.MaxBodyBytes = maxBody
	h.RegisterWebhookRoutes(mux)

	src := webhook.NewWebhookSource(fullPath)
	t.Cleanup(func() { _ = src.Close() })

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, fullPath, bytes.NewReader(body))
	for k, v := range header {
		req.Header[k] = v
	}
	mux.ServeHTTP(rec, req)
	return rec
}

func TestAWebhookBodyOverTheLimitIsRefused(t *testing.T) {
	const path = "/api/webhooks/limits/raw"
	store := newSavingStore(path, nil)

	rec := postLimited(t, store, path, 1024, bytes.Repeat([]byte("a"), 1025), nil)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("a body one byte over the limit was answered %d, want 413", rec.Code)
	}
	if n := store.count(); n != 0 {
		t.Errorf("a refused body was saved %d times", n)
	}

	if rec := postLimited(t, store, path, 1024, bytes.Repeat([]byte("a"), 1024), nil); rec.Code != http.StatusAccepted {
		t.Errorf("a body exactly at the limit was answered %d, want 202: %s", rec.Code, rec.Body.String())
	}
}

// A compressed body is held to the limit after it is inflated, not before:
// 8 MiB of zeros compresses to well under a kilobyte.
func TestAWebhookDecompressionBombIsRefused(t *testing.T) {
	const path = "/api/webhooks/limits/bomb"
	store := newSavingStore(path, nil)

	enc, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	bomb := enc.EncodeAll(make([]byte, 8<<20), nil)
	_ = enc.Close()
	if len(bomb) > 64<<10 {
		t.Fatalf("test setup: compressed body is %d bytes, expected it to be small", len(bomb))
	}

	rec := postLimited(t, store, path, 1<<20, bomb, http.Header{"Content-Encoding": {"zstd"}})
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("a body inflating to 8 MiB past a 1 MiB limit was answered %d, want 413", rec.Code)
	}
	if n := store.count(); n != 0 {
		t.Errorf("a refused body was saved %d times", n)
	}
}

func TestAWebhookThatFailsAuthenticationIsNotSaved(t *testing.T) {
	const path = "/api/webhooks/limits/keyed"
	store := newSavingStore(path, map[string]string{"api_key": "sesame"})

	rec := postLimited(t, store, path, 0, []byte(webhookBody), http.Header{"X-Api-Key": {"guess"}})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("a wrong key was answered %d, want 401", rec.Code)
	}
	if n := store.count(); n != 0 {
		t.Errorf("an unauthenticated request was saved %d times; anyone could fill the database", n)
	}
}

func TestAWebhookToAPathWithNoSourceIsNotSaved(t *testing.T) {
	store := newSavingStore("/api/webhooks/limits/real", nil)

	postLimited(t, store, "/api/webhooks/limits/made-up", 0, []byte(webhookBody), nil)
	if n := store.count(); n != 0 {
		t.Errorf("a request to a path no source holds was saved %d times; each made-up path keeps 50 more rows", n)
	}
}

func TestASavedWebhookKeepsNoCredentials(t *testing.T) {
	const path = "/api/webhooks/limits/redact"
	store := newSavingStore(path, map[string]string{"api_key": "sesame"})

	header := http.Header{
		"X-Api-Key":     {"sesame"},
		"Authorization": {"Bearer tok3n"},
		"Cookie":        {"hermod_session=abc"},
		"X-Request-Id":  {"r-1"},
	}
	if rec := postLimited(t, store, path, 0, []byte(webhookBody), header); rec.Code != http.StatusAccepted {
		t.Fatalf("an authenticated request was answered %d, want 202: %s", rec.Code, rec.Body.String())
	}
	if n := store.count(); n != 1 {
		t.Fatalf("an accepted request was saved %d times, want 1", n)
	}
	saved := store.saved[0]
	for k, v := range saved.Headers {
		for _, secret := range []string{"sesame", "tok3n", "abc"} {
			if strings.Contains(v, secret) {
				t.Errorf("saved header %s still carries a credential: %q", k, v)
			}
		}
	}
	if saved.Headers["X-Request-Id"] != "r-1" {
		t.Errorf("an ordinary header was not kept: %v", saved.Headers)
	}
}
