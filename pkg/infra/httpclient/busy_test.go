// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package httpclient

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
)

// 429 and 503 mean the other side is up and has no room. Every HTTP step and
// sink must report them the same way -- busy, with the wait the server asked
// for -- so the engine waits them out instead of failing the message.
func TestMarkBusyMarksOnlyTheStatusesThatMeanNoRoom(t *testing.T) {
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name       string
		status     int
		retryAfter string
		busy       bool
		wait       time.Duration
	}{
		{"429 with seconds", http.StatusTooManyRequests, "7", true, 7 * time.Second},
		{"503 with a date", http.StatusServiceUnavailable, now.Add(30 * time.Second).Format(http.TimeFormat), true, 30 * time.Second},
		{"429 without advice", http.StatusTooManyRequests, "", true, 0},
		{"429 with nonsense", http.StatusTooManyRequests, "soon", true, 0},
		{"500 is a failure", http.StatusInternalServerError, "7", false, 0},
		{"404 is a failure", http.StatusNotFound, "", false, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := &http.Response{StatusCode: c.status, Header: http.Header{}}
			if c.retryAfter != "" {
				resp.Header.Set("Retry-After", c.retryAfter)
			}
			cause := errors.New("status")
			err := markBusyAt(resp, cause, now)
			if !errors.Is(err, cause) {
				t.Fatalf("the cause was lost: %v", err)
			}
			if got := errors.Is(err, hermod.ErrBusy); got != c.busy {
				t.Fatalf("busy = %v, want %v", got, c.busy)
			}
			if !c.busy {
				return
			}
			b, _ := errors.AsType[*hermod.Busy](err)
			if b.RetryAfter != c.wait {
				t.Errorf("RetryAfter = %v, want %v", b.RetryAfter, c.wait)
			}
		})
	}
}

func TestMarkBusyLeavesNoErrorAlone(t *testing.T) {
	if err := MarkBusy(&http.Response{StatusCode: http.StatusTooManyRequests}, nil); err != nil {
		t.Errorf("MarkBusy(resp, nil) = %v, want nil", err)
	}
}
