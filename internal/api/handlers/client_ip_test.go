// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package handlers

import (
	"net/http/httptest"
	"testing"
)

func TestClientIP(t *testing.T) {
	tests := []struct {
		name   string
		remote string
		xff    string
		want   string
	}{
		{"direct, no header", "203.0.113.5:4444", "", "203.0.113.5"},
		// A client talking to Hermod directly must not choose its own IP.
		{"direct, forged header ignored", "203.0.113.5:4444", "198.51.100.1", "203.0.113.5"},
		// Behind a local proxy the header is believed.
		{"loopback proxy", "127.0.0.1:9000", "198.51.100.1", "198.51.100.1"},
		{"private proxy", "10.0.0.2:9000", "198.51.100.1", "198.51.100.1"},
		// The proxy appends the address it saw; whatever the client sent sits to
		// its left and is not trusted.
		{"proxy appends to forged value", "10.0.0.2:9000", "1.2.3.4, 198.51.100.1", "198.51.100.1"},
		// Proxies in a chain inside the private network are skipped.
		{"proxy chain", "10.0.0.2:9000", "198.51.100.1, 10.0.0.9", "198.51.100.1"},
		// Every hop private: the leftmost is the best guess left.
		{"all private", "127.0.0.1:9000", "192.168.1.20, 10.0.0.9", "192.168.1.20"},
		{"garbage header", "127.0.0.1:9000", "not-an-ip", "127.0.0.1"},
		{"ipv6 loopback proxy", "[::1]:9000", "2001:db8::7", "2001:db8::7"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/api/login", nil)
			r.RemoteAddr = tt.remote
			if tt.xff != "" {
				r.Header.Set("X-Forwarded-For", tt.xff)
			}
			if got := ClientIP(r); got != tt.want {
				t.Fatalf("ClientIP() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestClearLoginLockoutClearsEveryIPForTheUser(t *testing.T) {
	h := &Handler{}
	h.LoginAttempts.Store("alice|203.0.113.5", &LoginAttempt{Failures: MaxLoginAttempts})
	h.LoginAttempts.Store("alice|198.51.100.9", &LoginAttempt{Failures: 2})
	h.LoginAttempts.Store("alicia|203.0.113.5", &LoginAttempt{Failures: 1})

	if n := h.ClearLoginLockout("  Alice "); n != 2 {
		t.Fatalf("cleared %d entries, want 2", n)
	}
	for _, k := range []string{"alice|203.0.113.5", "alice|198.51.100.9"} {
		if _, ok := h.LoginAttempts.Load(k); ok {
			t.Fatalf("%s still present", k)
		}
	}
	if _, ok := h.LoginAttempts.Load("alicia|203.0.113.5"); !ok {
		t.Fatalf("another user's entry was cleared")
	}
}
