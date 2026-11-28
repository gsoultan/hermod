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
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gsoultan/hermod"
)

// MarkBusy reports err, the error for resp, as busy (hermod.Busy) when resp
// says the other side is up and has no room: 429 Too Many Requests or 503
// Service Unavailable. The wait it asks for with Retry-After goes with it, so
// the engine waits that long rather than failing the message. Any other status,
// and a nil err, is returned as it is.
func MarkBusy(resp *http.Response, err error) error {
	return markBusyAt(resp, err, time.Now())
}

func markBusyAt(resp *http.Response, err error, now time.Time) error {
	if err == nil || resp == nil || !noRoom(resp.StatusCode) {
		return err
	}
	return &hermod.Busy{Err: err, RetryAfter: RetryAfter(resp.Header.Get("Retry-After"), now)}
}

func noRoom(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable
}

// RetryAfter reads a Retry-After value, either seconds or an HTTP date, as a
// wait from now. Anything else, or a time already past, is zero: no advice.
func RetryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		return max(time.Duration(secs)*time.Second, 0)
	}
	if at, err := http.ParseTime(v); err == nil {
		return max(at.Sub(now), 0)
	}
	return 0
}
