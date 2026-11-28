// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package lookup

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
)

// An API that answers 429 or 503 is up and has no room for this caller. The
// lookup used to spend its retries on it at a fixed delay, ignoring the
// Retry-After the API sent, and then fail the message -- or, with a default
// value configured, write the default into it as if the API had nothing to
// say. Under heavy traffic that turned "the API asked us to slow down" into
// failed messages or quietly wrong data.
//
// It now reports the answer as busy, once, with the API's own wait, and the
// engine waits it out before trying the message again.
func TestAPILookupReportsANoRoomAnswerAsBusy(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Retry-After", "9")
				w.WriteHeader(status)
			}))
			t.Cleanup(srv.Close)

			got, err := apiLookupErr(t, map[string]any{
				"method": "GET", "url": srv.URL + "/c/1",
				"targetField": "tier", "defaultValue": "unknown",
				"maxRetries": "3", "retryDelay": "1ms",
			}, nil)

			if !errors.Is(err, hermod.ErrBusy) {
				t.Fatalf("err = %v, want a busy answer the engine waits out", err)
			}
			b, _ := errors.AsType[*hermod.Busy](err)
			if b.RetryAfter != 9*time.Second {
				t.Errorf("RetryAfter = %v, want the 9s the API asked for", b.RetryAfter)
			}
			if n := calls.Load(); n != 1 {
				t.Errorf("the API was called %d times; a busy answer must not be retried on the spot", n)
			}
			if got != nil && got["tier"] == "unknown" {
				t.Error("the default value was written as if the API had answered")
			}
		})
	}
}
