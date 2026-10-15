// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package twitter

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gsoultan/hermod/pkg/comm/message"
)

func TestTwitterSink_Write(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tweets" {
			t.Errorf("Expected path /tweets, got %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("Expected Bearer test-token, got %s", r.Header.Get("Authorization"))
		}
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"data": {"id": "123", "text": "Hello"}}`))
	}))
	defer server.Close()

	sink := NewTwitterSink("test-token", nil)
	sink.baseURL = server.URL

	msg := message.AcquireMessage()
	msg.SetPayload([]byte("Hello World"))

	err := sink.Write(t.Context(), msg)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
}

func TestTwitterSink_Ping(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/users/me" {
			t.Errorf("Expected path /users/me, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sink := NewTwitterSink("test-token", nil)
	sink.baseURL = server.URL

	err := sink.Ping(t.Context())
	if err != nil {
		t.Fatalf("Ping failed: %v", err)
	}
}
