// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package discord

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gsoultan/hermod/pkg/comm/message"
)

func TestDiscordSink_Write_Webhook(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sink := NewDiscordSink(server.URL, "", "", nil)
	msg := message.AcquireMessage()
	msg.SetPayload([]byte("test message"))

	err := sink.Write(t.Context(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDiscordSink_Write_Bot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bot test-token" {
			t.Errorf("expected Bot test-token, got %s", r.Header.Get("Authorization"))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sink := NewDiscordSink("", "test-token", "123", nil)
	sink.baseURL = server.URL
	msg := message.AcquireMessage()
	msg.SetPayload([]byte("test message"))

	err := sink.Write(t.Context(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
