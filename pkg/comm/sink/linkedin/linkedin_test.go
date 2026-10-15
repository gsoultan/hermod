// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package linkedin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gsoultan/hermod/pkg/comm/message"
)

func TestLinkedInSink_Write(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ugcPosts" {
			t.Errorf("Expected path /ugcPosts, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id": "urn:li:share:123"}`))
	}))
	defer server.Close()

	sink := NewLinkedInSink("test-token", "urn:li:person:123", nil)
	sink.baseURL = server.URL

	msg := message.AcquireMessage()
	msg.SetPayload([]byte("Hello LinkedIn"))

	err := sink.Write(t.Context(), msg)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
}

func TestLinkedInSink_Ping(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/me" {
			t.Errorf("Expected path /me, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sink := NewLinkedInSink("test-token", "urn:li:person:123", nil)
	sink.baseURL = server.URL

	err := sink.Ping(t.Context())
	if err != nil {
		t.Fatalf("Ping failed: %v", err)
	}
}
