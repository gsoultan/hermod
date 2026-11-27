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
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
)

// 429 and 503 say the receiver is up and has no room: the engine waits them
// out instead of spending retries or tripping the breaker, and only if the
// sink says so. They used to be "unexpected status code: 429", which the engine
// could not tell from a receiver that was down, so an API's rate limit put the
// flow into the dead letter within a fraction of a second.
func TestBusyStatusesAreReportedAsBusy(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		retryAfter string
		busy       bool
		wait       time.Duration
	}{
		{"429 with seconds", http.StatusTooManyRequests, "7", true, 7 * time.Second},
		{"503 with no Retry-After", http.StatusServiceUnavailable, "", true, 0},
		{"429 with a date", http.StatusTooManyRequests, time.Now().Add(20 * time.Second).UTC().Format(http.TimeFormat), true, 20 * time.Second},
		{"429 with nonsense", http.StatusTooManyRequests, "soon", true, 0},
		{"500 is not busy", http.StatusInternalServerError, "", false, 0},
		{"400 is not busy", http.StatusBadRequest, "", false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tc.retryAfter != "" {
					w.Header().Set("Retry-After", tc.retryAfter)
				}
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()
			s := NewHttpSink(srv.URL, &mockFormatter{}, nil)

			for _, err := range []error{
				s.Write(context.Background(), &mockMessage{id: "1"}),
				s.WriteBatch(context.Background(), []hermod.Message{&mockMessage{id: "1"}}),
			} {
				if err == nil {
					t.Fatal("no error for a refused request")
				}
				if got := errors.Is(err, hermod.ErrBusy); got != tc.busy {
					t.Fatalf("busy = %v for %q, want %v", got, err, tc.busy)
				}
				d, _ := hermod.RetryAfter(err)
				if d < tc.wait-2*time.Second || d > tc.wait {
					t.Errorf("Retry-After read as %s, want about %s", d, tc.wait)
				}
			}
		})
	}
}
