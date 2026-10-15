// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

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
