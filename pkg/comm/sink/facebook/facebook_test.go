// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package facebook

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gsoultan/hermod/pkg/comm/message"
)

func TestFacebookSink_Write(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/test-page/feed" {
			t.Errorf("Expected path /test-page/feed, got %s", r.URL.Path)
		}
		if r.URL.Query().Get("access_token") != "test-token" {
			t.Errorf("Expected token test-token, got %s", r.URL.Query().Get("access_token"))
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id": "post_123"}`))
	}))
	defer server.Close()

	sink := NewFacebookSink("test-token", "test-page", nil)
	sink.baseURL = server.URL

	msg := message.AcquireMessage()
	msg.SetPayload([]byte("Hello Facebook"))

	err := sink.Write(t.Context(), msg)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
}

func TestFacebookSink_Ping(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/test-page" {
			t.Errorf("Expected path /test-page, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sink := NewFacebookSink("test-token", "test-page", nil)
	sink.baseURL = server.URL

	err := sink.Ping(t.Context())
	if err != nil {
		t.Fatalf("Ping failed: %v", err)
	}
}
