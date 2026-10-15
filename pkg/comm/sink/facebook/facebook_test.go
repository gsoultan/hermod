// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

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
