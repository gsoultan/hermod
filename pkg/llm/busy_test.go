// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package llm

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
)

// A provider that is rate limiting (429) or overloaded (503, and Anthropic's
// 529) is up and has no room. WithRetry already waits a few times; once it
// gives up the error reached the workflow as a plain failure, so every AI step
// failed its message under a rate limit. It is busy now, with the provider's
// Retry-After, and the engine waits it out.
func TestAPIErrorIsBusyOnlyWhenTheProviderHasNoRoom(t *testing.T) {
	cases := []struct {
		status int
		busy   bool
	}{
		{429, true}, {503, true}, {529, true},
		{500, false}, {400, false}, {401, false}, {0, false},
	}
	for _, c := range cases {
		t.Run(strconv.Itoa(c.status), func(t *testing.T) {
			err := fmt.Errorf("chat: %w", &APIError{Provider: "p", Status: c.status, RetryAfter: 3 * time.Second})
			if got := errors.Is(err, hermod.ErrBusy); got != c.busy {
				t.Fatalf("busy = %v, want %v", got, c.busy)
			}
			d, ok := hermod.RetryAfter(err)
			if c.busy && (!ok || d != 3*time.Second) {
				t.Errorf("RetryAfter = %v, %v; want the provider's 3s", d, ok)
			}
			if !c.busy && ok {
				t.Errorf("a failure reported a busy wait of %v", d)
			}
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.Status != c.status {
				t.Error("the APIError itself is no longer reachable")
			}
		})
	}
}

// OpenAI answers an account that is out of credit with 429 too, code
// insufficient_quota. That is not "come back later": waiting it out would stall
// the workflow until someone pays, with nothing saying why. It stays a failure,
// and is not retried either.
func TestAnOutOfQuotaAnswerIsAFailureNotBusy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"You exceeded your current quota","type":"insufficient_quota","code":"insufficient_quota"}}`))
	}))
	t.Cleanup(srv.Close)

	var out map[string]any
	_, err := PostJSON(t.Context(), srv.Client(), "openai", srv.URL, nil, map[string]any{}, &out)
	if err == nil {
		t.Fatal("an out-of-quota answer succeeded")
	}
	if errors.Is(err, hermod.ErrBusy) {
		t.Error("an out-of-quota answer is reported busy, so the workflow would wait for it forever")
	}
	if IsRetryable(err) {
		t.Error("an out-of-quota answer is retried")
	}
}
